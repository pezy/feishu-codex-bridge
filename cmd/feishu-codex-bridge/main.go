package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pezy/feishu-codex-bridge/internal/app"
	"github.com/pezy/feishu-codex-bridge/internal/config"
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("build app: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Start(ctx)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatalf("application stopped: %v", err)
		}
	case <-ctx.Done():
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()

	if err := application.Close(shutdownCtx); err != nil {
		log.Fatalf("shutdown app: %v", err)
	}
}
