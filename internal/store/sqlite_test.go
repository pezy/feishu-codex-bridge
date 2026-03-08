package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStoreTracksMessagesAndExecutions(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "bridge.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()

	message := MessageRecord{
		MessageID:    "om_123",
		ChatID:       "oc_123",
		SenderOpenID: "ou_123",
		TextContent:  "hello",
		Status:       "received",
		CreatedAt:    time.Now().UTC(),
	}

	inserted, err := store.TryCreateInbound(ctx, message)
	if err != nil {
		t.Fatalf("TryCreateInbound: %v", err)
	}
	if !inserted {
		t.Fatalf("expected insert to succeed")
	}

	inserted, err = store.TryCreateInbound(ctx, message)
	if err != nil {
		t.Fatalf("TryCreateInbound duplicate: %v", err)
	}
	if inserted {
		t.Fatalf("expected duplicate insert to be ignored")
	}

	entry := ConversationEntry{
		Source:    "user",
		OpenID:    "ou_123",
		MessageID: "om_123",
		Content:   "hello",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.AppendConversation(ctx, entry); err != nil {
		t.Fatalf("AppendConversation: %v", err)
	}

	execRecord := ExecutionRecord{
		ID:               "exec_123",
		RequestMessageID: "om_123",
		Prompt:           "prompt",
		Status:           "running",
		StartedAt:        time.Now().UTC(),
	}
	if err := store.CreateExecution(ctx, execRecord); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	completedAt := time.Now().UTC()
	execRecord.Status = "completed"
	execRecord.Output = "done"
	execRecord.CompletedAt = &completedAt
	if err := store.FinishExecution(ctx, execRecord); err != nil {
		t.Fatalf("FinishExecution: %v", err)
	}

	recent, err := store.RecentConversations(ctx, 10)
	if err != nil {
		t.Fatalf("RecentConversations: %v", err)
	}
	if len(recent) != 1 || recent[0].Content != "hello" {
		t.Fatalf("unexpected recent conversations: %+v", recent)
	}

	lastExecution, err := store.LastExecution(ctx)
	if err != nil {
		t.Fatalf("LastExecution: %v", err)
	}
	if lastExecution == nil || lastExecution.Output != "done" {
		t.Fatalf("unexpected last execution: %+v", lastExecution)
	}
}
