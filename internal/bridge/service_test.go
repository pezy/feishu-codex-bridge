package bridge

import (
	"context"
	"testing"
	"time"

	"github.com/pezy/feishu-codex-bridge/internal/config"
	"github.com/pezy/feishu-codex-bridge/internal/store"
)

func TestMaskOpenID(t *testing.T) {
	if got := maskOpenID("ou_1234567890"); got != "ou_1...7890" {
		t.Fatalf("unexpected masked id: %s", got)
	}
}

func TestStatusFallsBackToStoreExecution(t *testing.T) {
	sqliteStore, err := store.NewSQLiteStore(t.TempDir() + "/bridge.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() {
		_ = sqliteStore.Close()
	}()

	startedAt := time.Now().UTC()
	if err := sqliteStore.CreateExecution(context.Background(), store.ExecutionRecord{
		ID:               "exec_1",
		RequestMessageID: "om_1",
		Prompt:           "prompt",
		Status:           "running",
		StartedAt:        startedAt,
	}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	service := &Service{
		store: sqliteStore,
		cfg: config.Config{
			HTTPAddr:           "127.0.0.1:8787",
			DefaultWorkDir:     "/tmp/service",
			AuthorizedOpenID:   "ou_1234567890",
			RecentContextLimit: 12,
		},
	}

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.LastExecution == nil || status.LastExecution.RequestMessageID != "om_1" {
		t.Fatalf("unexpected last execution: %+v", status.LastExecution)
	}
}
