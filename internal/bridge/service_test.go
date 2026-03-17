package bridge

import (
	"context"
	"testing"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
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

func TestParseIncomingP2PText(t *testing.T) {
	incoming, err := parseIncoming(buildEvent("p2p", "text", `{"text":"hello"}`, nil))
	if err != nil {
		t.Fatalf("parseIncoming: %v", err)
	}
	if incoming.Text != "hello" || incoming.ChatType != "p2p" || incoming.MessageType != "text" {
		t.Fatalf("unexpected incoming: %+v", incoming)
	}
}

func TestParseIncomingGroupMentionText(t *testing.T) {
	incoming, err := parseIncoming(buildEvent("group", "text", `{"text":"@bot hi"}`, []*larkim.MentionEvent{{Key: stringPtr("@_user_1")}}))
	if err != nil {
		t.Fatalf("parseIncoming: %v", err)
	}
	if !incoming.HasMentions {
		t.Fatalf("expected group message to carry mentions")
	}
}

func TestParseIncomingRejectsGroupImage(t *testing.T) {
	_, err := parseIncoming(buildEvent("group", "image", `{"image_key":"img_123"}`, nil))
	if !isIgnoredEvent(err) {
		t.Fatalf("expected ignored error, got %v", err)
	}
}

func TestParseResponsePayload(t *testing.T) {
	payload := parseResponsePayload("line1\n[[image:/tmp/a.png]]\nline2\n[[image:/tmp/b.png]]")
	if payload.Text != "line1\nline2" {
		t.Fatalf("unexpected text: %q", payload.Text)
	}
	if len(payload.ImagePaths) != 2 || payload.ImagePaths[0] != "/tmp/a.png" || payload.ImagePaths[1] != "/tmp/b.png" {
		t.Fatalf("unexpected image paths: %#v", payload.ImagePaths)
	}
}

func TestParseResponsePayloadWithWikiWrite(t *testing.T) {
	payload := parseResponsePayload("done\n[[wiki-write:https://example.feishu.cn/wiki/abc]]\n# title\nbody\n[[/wiki-write]]")
	if payload.Text != "done" {
		t.Fatalf("unexpected text: %q", payload.Text)
	}
	if len(payload.WikiWrites) != 1 {
		t.Fatalf("unexpected wiki writes: %#v", payload.WikiWrites)
	}
	if payload.WikiWrites[0].URL != "https://example.feishu.cn/wiki/abc" {
		t.Fatalf("unexpected wiki url: %#v", payload.WikiWrites[0])
	}
	if payload.WikiWrites[0].Markdown != "# title\nbody" {
		t.Fatalf("unexpected wiki markdown: %#v", payload.WikiWrites[0])
	}
}

func TestPairingApprovalReplyText(t *testing.T) {
	got := pairingApprovalReplyText("127.0.0.1:8787", "ou_123")
	want := "配对申请已收到。请在 server 主机执行以下命令完成授权：\ncurl -X POST http://127.0.0.1:8787/v1/pairing/requests/ou_123/approve"
	if got != want {
		t.Fatalf("unexpected pairing reply:\n%s", got)
	}
}

func TestAuthorizeAndRemoveGroup(t *testing.T) {
	sqliteStore, err := store.NewSQLiteStore(t.TempDir() + "/bridge.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() {
		_ = sqliteStore.Close()
	}()

	service := &Service{store: sqliteStore}
	ctx := context.Background()

	if err := service.AuthorizeGroup(ctx, "oc_group_1"); err != nil {
		t.Fatalf("AuthorizeGroup: %v", err)
	}
	authorized, err := sqliteStore.IsAuthorizedGroup(ctx, "oc_group_1")
	if err != nil {
		t.Fatalf("IsAuthorizedGroup: %v", err)
	}
	if !authorized {
		t.Fatalf("expected group to be authorized")
	}

	if err := service.RemoveAuthorizedGroup(ctx, "oc_group_1"); err != nil {
		t.Fatalf("RemoveAuthorizedGroup: %v", err)
	}
	authorized, err = sqliteStore.IsAuthorizedGroup(ctx, "oc_group_1")
	if err != nil {
		t.Fatalf("IsAuthorizedGroup after remove: %v", err)
	}
	if authorized {
		t.Fatalf("expected group to be removed")
	}
}

func TestHandleIncomingGroupTextWithoutMentionRecordsContextOnly(t *testing.T) {
	sqliteStore, err := store.NewSQLiteStore(t.TempDir() + "/bridge.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() {
		_ = sqliteStore.Close()
	}()

	service := &Service{
		store: sqliteStore,
		cfg: config.Config{
			RecentContextLimit: 12,
		},
	}

	event := buildEvent("group", "text", `{"text":"前情提要"}`, nil)
	if err := service.HandleIncomingMessage(context.Background(), event); err != nil {
		t.Fatalf("HandleIncomingMessage: %v", err)
	}

	history, err := sqliteStore.RecentConversationsByChat(context.Background(), "oc_123", 10)
	if err != nil {
		t.Fatalf("RecentConversationsByChat: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected one recorded context entry, got %+v", history)
	}
	if history[0].Content != "前情提要" || history[0].ChatType != "group" {
		t.Fatalf("unexpected recorded entry: %+v", history[0])
	}

	lastExecution, err := sqliteStore.LastExecution(context.Background())
	if err != nil {
		t.Fatalf("LastExecution: %v", err)
	}
	if lastExecution != nil {
		t.Fatalf("expected no execution for passive group context, got %+v", lastExecution)
	}
}

func buildEvent(chatType string, messageType string, content string, mentions []*larkim.MentionEvent) *larkim.P2MessageReceiveV1 {
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageId:   stringPtr("om_123"),
				ChatId:      stringPtr("oc_123"),
				ChatType:    stringPtr(chatType),
				MessageType: stringPtr(messageType),
				Content:     stringPtr(content),
				Mentions:    mentions,
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: stringPtr("ou_123"),
				},
			},
		},
	}
}

func stringPtr(input string) *string {
	return &input
}
