package bridge

import (
	"context"
	"errors"
	"testing"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pezy/feishu-codex-bridge/internal/config"
	"github.com/pezy/feishu-codex-bridge/internal/feishu"
	"github.com/pezy/feishu-codex-bridge/internal/store"
)

type fakeFeishuClient struct {
	listChatMessages func(context.Context, string, string, int) ([]feishu.ChatMessage, error)
	getBotNames      func(context.Context) ([]string, error)
}

func (f *fakeFeishuClient) SendTextToOpenID(context.Context, string, string) (string, error) {
	return "", nil
}

func (f *fakeFeishuClient) SendImageToOpenID(context.Context, string, string) (string, error) {
	return "", nil
}

func (f *fakeFeishuClient) ReplyText(context.Context, string, string) (string, error) {
	return "", nil
}

func (f *fakeFeishuClient) ReplyImage(context.Context, string, string) (string, error) {
	return "", nil
}

func (f *fakeFeishuClient) AddReaction(context.Context, string, string) (string, error) {
	return "", nil
}

func (f *fakeFeishuClient) DeleteReaction(context.Context, string, string) error {
	return nil
}

func (f *fakeFeishuClient) DownloadMessageImage(context.Context, string, string) (feishu.MessageImage, error) {
	return feishu.MessageImage{}, nil
}

func (f *fakeFeishuClient) WriteWikiMarkdown(context.Context, string, string) error {
	return nil
}

func (f *fakeFeishuClient) ListChatMessages(ctx context.Context, chatID string, endTime string, pageSize int) ([]feishu.ChatMessage, error) {
	if f.listChatMessages == nil {
		return nil, nil
	}
	return f.listChatMessages(ctx, chatID, endTime, pageSize)
}

func (f *fakeFeishuClient) GetBotMentionNames(ctx context.Context) ([]string, error) {
	if f.getBotNames == nil {
		return nil, nil
	}
	return f.getBotNames(ctx)
}

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
	incoming, err := parseIncoming(buildEvent("group", "text", `{"text":"@bot hi"}`, []*larkim.MentionEvent{{Key: stringPtr("@_user_1"), Name: stringPtr("bot")}}))
	if err != nil {
		t.Fatalf("parseIncoming: %v", err)
	}
	if !incoming.HasMentions || len(incoming.Mentions) != 1 {
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
	// No passive context recording for group messages without @ mention
	if len(history) != 0 {
		t.Fatalf("expected no recorded context entry, got %+v", history)
	}

	lastExecution, err := sqliteStore.LastExecution(context.Background())
	if err != nil {
		t.Fatalf("LastExecution: %v", err)
	}
	if lastExecution != nil {
		t.Fatalf("expected no execution for passive group context, got %+v", lastExecution)
	}
}

func TestHandleIncomingGroupTextMentioningOtherUserRecordsContextOnly(t *testing.T) {
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
			AppID:              "cli_bot_app",
			RecentContextLimit: 12,
		},
		feishu: &fakeFeishuClient{
			getBotNames: func(context.Context) ([]string, error) {
				return []string{"Codex Bot"}, nil
			},
		},
	}

	event := buildEvent("group", "text", `{"text":"@Alice 帮我看下"}`, []*larkim.MentionEvent{{
		Key:  stringPtr("@_user_1"),
		Name: stringPtr("Alice"),
		Id: &larkim.UserId{
			OpenId: stringPtr("ou_alice"),
		},
	}})
	if err := service.HandleIncomingMessage(context.Background(), event); err != nil {
		t.Fatalf("HandleIncomingMessage: %v", err)
	}

	history, err := sqliteStore.RecentConversationsByChat(context.Background(), "oc_123", 10)
	if err != nil {
		t.Fatalf("RecentConversationsByChat: %v", err)
	}
	// No passive context recording for group messages without @ bot mention
	if len(history) != 0 {
		t.Fatalf("expected no recorded context entry, got %+v", history)
	}

	lastExecution, err := sqliteStore.LastExecution(context.Background())
	if err != nil {
		t.Fatalf("LastExecution: %v", err)
	}
	if lastExecution != nil {
		t.Fatalf("expected no execution when mentioning another user, got %+v", lastExecution)
	}
}

func TestMessageMentionsBot(t *testing.T) {
	mentions := []*larkim.MentionEvent{
		{
			Name: stringPtr("Codex Bot"),
		},
		{
			Name: stringPtr("Alice"),
			Id: &larkim.UserId{
				OpenId: stringPtr("ou_alice"),
			},
		},
	}

	if !messageMentionsBot(mentions, []string{"Codex Bot", "Codex 机器人"}, "cli_bot_app") {
		t.Fatalf("expected bot mention to match")
	}
	if messageMentionsBot(mentions[1:], []string{"Codex Bot"}, "cli_bot_app") {
		t.Fatalf("expected other user mention not to match bot")
	}
	if !messageMentionsBot([]*larkim.MentionEvent{{
		Id: &larkim.UserId{
			UserId: stringPtr("cli_bot_app"),
		},
	}}, nil, "cli_bot_app") {
		t.Fatalf("expected app id mention to match")
	}
}

func TestBuildPromptHistoryUsesRemoteGroupMessages(t *testing.T) {
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
			RecentContextLimit: 2,
		},
		feishu: &fakeFeishuClient{
			listChatMessages: func(ctx context.Context, chatID string, endTime string, pageSize int) ([]feishu.ChatMessage, error) {
				return []feishu.ChatMessage{
					{
						MessageID:  "om_current",
						ChatID:     chatID,
						SenderID:   "ou_current",
						SenderType: "user",
						SenderRole: "open_id",
						MsgType:    "text",
						Content:    `{"text":"@bot 看一下"}`,
						CreatedAt:  time.Unix(1700000200, 0).UTC(),
					},
					{
						MessageID:  "om_2",
						ChatID:     chatID,
						SenderID:   "ou_2",
						SenderType: "user",
						SenderRole: "open_id",
						MsgType:    "text",
						Content:    `{"text":"第二条"}`,
						CreatedAt:  time.Unix(1700000100, 0).UTC(),
					},
					{
						MessageID:  "om_1",
						ChatID:     chatID,
						SenderType: "app",
						SenderRole: "app_id",
						MsgType:    "text",
						Content:    `{"text":"第一条回复"}`,
						CreatedAt:  time.Unix(1700000000, 0).UTC(),
					},
				}, nil
			},
		},
	}
	incoming := &incomingMessage{
		Message: &larkim.EventMessage{
			CreateTime: stringPtr("1700000200000"),
		},
		MessageID:   "om_current",
		ChatID:      "oc_123",
		ChatType:    "group",
		HasMentions: true,
	}

	history, err := service.buildPromptHistory(context.Background(), incoming)
	if err != nil {
		t.Fatalf("buildPromptHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("unexpected history len: %+v", history)
	}
	if history[0].MessageID != "om_1" || history[0].Source != "assistant" {
		t.Fatalf("unexpected first history item: %+v", history[0])
	}
	if history[1].MessageID != "om_2" || history[1].OpenID != "ou_2" {
		t.Fatalf("unexpected second history item: %+v", history[1])
	}
}

func TestBuildPromptHistoryFallsBackToLocalWhenRemoteFails(t *testing.T) {
	sqliteStore, err := store.NewSQLiteStore(t.TempDir() + "/bridge.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() {
		_ = sqliteStore.Close()
	}()

	if err := sqliteStore.AppendConversation(context.Background(), store.ConversationEntry{
		Source:      "user",
		OpenID:      "ou_local",
		ChatID:      "oc_123",
		ChatType:    "group",
		MessageID:   "om_local",
		Content:     "本地上下文",
		ContentType: "text",
		CreatedAt:   time.Unix(1700000000, 0).UTC(),
	}); err != nil {
		t.Fatalf("AppendConversation: %v", err)
	}

	service := &Service{
		store: sqliteStore,
		cfg: config.Config{
			RecentContextLimit: 2,
		},
		feishu: &fakeFeishuClient{
			listChatMessages: func(ctx context.Context, chatID string, endTime string, pageSize int) ([]feishu.ChatMessage, error) {
				return nil, errors.New("boom")
			},
		},
	}
	incoming := &incomingMessage{
		MessageID:   "om_current",
		ChatID:      "oc_123",
		ChatType:    "group",
		HasMentions: true,
	}

	history, err := service.buildPromptHistory(context.Background(), incoming)
	if err != nil {
		t.Fatalf("buildPromptHistory: %v", err)
	}
	if len(history) != 1 || history[0].MessageID != "om_local" {
		t.Fatalf("unexpected fallback history: %+v", history)
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
