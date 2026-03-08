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
		MessageID:      "om_123",
		ChatID:         "oc_123",
		ChatType:       "p2p",
		MessageType:    "text",
		SenderOpenID:   "ou_123",
		TextContent:    "hello",
		RawContentJSON: `{"text":"hello"}`,
		Status:         "received",
		CreatedAt:      time.Now().UTC(),
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
		Source:      "user",
		OpenID:      "ou_123",
		MessageID:   "om_123",
		Content:     "hello",
		ContentType: "text",
		CreatedAt:   time.Now().UTC(),
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

	if err := store.EnsureAuthorizedUser(ctx, "ou_123"); err != nil {
		t.Fatalf("EnsureAuthorizedUser: %v", err)
	}
	authorized, err := store.IsAuthorizedUser(ctx, "ou_123")
	if err != nil {
		t.Fatalf("IsAuthorizedUser: %v", err)
	}
	if !authorized {
		t.Fatalf("expected user to be authorized")
	}

	if err := store.CreateOrRefreshPairingRequest(ctx, "ou_456"); err != nil {
		t.Fatalf("CreateOrRefreshPairingRequest: %v", err)
	}
	requests, err := store.ListPendingPairingRequests(ctx)
	if err != nil {
		t.Fatalf("ListPendingPairingRequests: %v", err)
	}
	if len(requests) != 1 || requests[0].OpenID != "ou_456" {
		t.Fatalf("unexpected pairing requests: %+v", requests)
	}
	if err := store.SetPairingRequestStatus(ctx, "ou_456", "approved"); err != nil {
		t.Fatalf("SetPairingRequestStatus: %v", err)
	}

	if err := store.EnsureAuthorizedGroup(ctx, "oc_group_1"); err != nil {
		t.Fatalf("EnsureAuthorizedGroup: %v", err)
	}
	groupAuthorized, err := store.IsAuthorizedGroup(ctx, "oc_group_1")
	if err != nil {
		t.Fatalf("IsAuthorizedGroup: %v", err)
	}
	if !groupAuthorized {
		t.Fatalf("expected group to be authorized")
	}
	if err := store.RemoveAuthorizedGroup(ctx, "oc_group_1"); err != nil {
		t.Fatalf("RemoveAuthorizedGroup: %v", err)
	}
	groupAuthorized, err = store.IsAuthorizedGroup(ctx, "oc_group_1")
	if err != nil {
		t.Fatalf("IsAuthorizedGroup after delete: %v", err)
	}
	if groupAuthorized {
		t.Fatalf("expected group to be removed")
	}
}
