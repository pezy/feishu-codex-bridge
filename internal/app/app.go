package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"github.com/pezy/feishu-codex-bridge/internal/bridge"
	"github.com/pezy/feishu-codex-bridge/internal/codex"
	"github.com/pezy/feishu-codex-bridge/internal/config"
	"github.com/pezy/feishu-codex-bridge/internal/feishu"
	"github.com/pezy/feishu-codex-bridge/internal/httpapi"
	"github.com/pezy/feishu-codex-bridge/internal/store"
)

type App struct {
	cfg      config.Config
	store    *store.SQLiteStore
	server   *httpapi.Server
	wsClient *larkws.Client
	service  *bridge.Service
}

func New(cfg config.Config) (*App, error) {
	sqliteStore, err := store.NewSQLiteStore(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	feishuClient := feishu.New(cfg.AppID, cfg.AppSecret)
	runner := codex.NewRunner(cfg.CodexPath, cfg.DefaultWorkDir, cfg.CodexTimeout)
	service := bridge.New(cfg, sqliteStore, feishuClient, runner)

	server := httpapi.New(cfg.HTTPAddr, cfg.ReadTimeout, cfg.WriteTimeout, service)
	wsClient := feishu.NewWSClient(cfg.AppID, cfg.AppSecret, service, toLogLevel(cfg.LogLevel))

	return &App{
		cfg:      cfg,
		store:    sqliteStore,
		server:   server,
		wsClient: wsClient,
		service:  service,
	}, nil
}

func (a *App) Start(ctx context.Context) error {
	a.service.MarkWSRunning()

	serverErrCh := make(chan error, 1)
	go func() {
		if err := a.server.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
	}()

	wsErrCh := make(chan error, 1)
	go func() {
		if err := a.wsClient.Start(ctx); err != nil {
			wsErrCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-serverErrCh:
		return fmt.Errorf("http server: %w", err)
	case err := <-wsErrCh:
		return fmt.Errorf("feishu ws: %w", err)
	}
}

func (a *App) Close(ctx context.Context) error {
	var errs []string
	if err := a.server.Close(ctx); err != nil {
		errs = append(errs, err.Error())
	}
	if err := a.store.Close(); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("close app: %s", strings.Join(errs, "; "))
	}
	return nil
}

func toLogLevel(input string) larkcore.LogLevel {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "debug":
		return larkcore.LogLevelDebug
	case "warn":
		return larkcore.LogLevelWarn
	case "error":
		return larkcore.LogLevelError
	default:
		return larkcore.LogLevelInfo
	}
}
