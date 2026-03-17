package feishu

import (
	"context"
	"testing"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

func TestAddReactionRefreshesInvalidTenantToken(t *testing.T) {
	now := time.Unix(100, 0)
	tokenFetches := 0
	callTokens := make([]string, 0, 2)

	client := &Client{
		appID:     "cli_xxx",
		appSecret: "secret_xxx",
		now:       func() time.Time { return now },
		getTenantAccessToken: func(ctx context.Context, req *larkcore.SelfBuiltTenantAccessTokenReq) (*larkcore.TenantAccessTokenResp, error) {
			tokenFetches++
			return &larkcore.TenantAccessTokenResp{
				ApiResp: &larkcore.ApiResp{},
				CodeError: larkcore.CodeError{
					Code: 0,
				},
				Expire:            7200,
				TenantAccessToken: "fresh-token",
			}, nil
		},
		addReaction: func(ctx context.Context, messageID string, emojiType string, tenantAccessToken string) (string, error) {
			callTokens = append(callTokens, tenantAccessToken)
			if tenantAccessToken == "stale-token" {
				return "", newAPIError("create message reaction", errCodeTenantAccessTokenInvalid, "invalid", "req-stale")
			}
			return "reaction_1", nil
		},
	}
	client.tenantAccessToken = "stale-token"
	client.tenantAccessTokenExpireAt = now.Add(time.Hour)

	reactionID, err := client.AddReaction(context.Background(), "om_123", "Typing")
	if err != nil {
		t.Fatalf("AddReaction: %v", err)
	}
	if reactionID != "reaction_1" {
		t.Fatalf("unexpected reaction id: %s", reactionID)
	}
	if tokenFetches != 1 {
		t.Fatalf("unexpected token fetches: %d", tokenFetches)
	}
	if len(callTokens) != 2 || callTokens[0] != "stale-token" || callTokens[1] != "fresh-token" {
		t.Fatalf("unexpected call tokens: %#v", callTokens)
	}
}

func TestReplyTextReusesCachedTenantToken(t *testing.T) {
	now := time.Unix(200, 0)
	tokenFetches := 0
	callTokens := make([]string, 0, 2)

	client := &Client{
		appID:     "cli_xxx",
		appSecret: "secret_xxx",
		now:       func() time.Time { return now },
		getTenantAccessToken: func(ctx context.Context, req *larkcore.SelfBuiltTenantAccessTokenReq) (*larkcore.TenantAccessTokenResp, error) {
			tokenFetches++
			return &larkcore.TenantAccessTokenResp{
				ApiResp: &larkcore.ApiResp{},
				CodeError: larkcore.CodeError{
					Code: 0,
				},
				Expire:            7200,
				TenantAccessToken: "cached-token",
			}, nil
		},
		replyText: func(ctx context.Context, messageID string, text string, tenantAccessToken string) (string, error) {
			callTokens = append(callTokens, tenantAccessToken)
			return "om_reply", nil
		},
	}

	for range 2 {
		if _, err := client.ReplyText(context.Background(), "om_123", "hello"); err != nil {
			t.Fatalf("ReplyText: %v", err)
		}
	}

	if tokenFetches != 1 {
		t.Fatalf("unexpected token fetches: %d", tokenFetches)
	}
	if len(callTokens) != 2 || callTokens[0] != "cached-token" || callTokens[1] != "cached-token" {
		t.Fatalf("unexpected call tokens: %#v", callTokens)
	}
}

func TestTenantAccessTokenTTLUsesSafetyWindow(t *testing.T) {
	if ttl := tenantAccessTokenTTL(7200); ttl != 117*time.Minute {
		t.Fatalf("unexpected ttl: %s", ttl)
	}
	if ttl := tenantAccessTokenTTL(60); ttl != time.Second {
		t.Fatalf("unexpected short ttl: %s", ttl)
	}
}

func TestWriteWikiMarkdownReusesCachedTenantToken(t *testing.T) {
	now := time.Unix(300, 0)
	tokenFetches := 0
	callTokens := make([]string, 0, 2)

	client := &Client{
		appID:     "cli_xxx",
		appSecret: "secret_xxx",
		now:       func() time.Time { return now },
		getTenantAccessToken: func(ctx context.Context, req *larkcore.SelfBuiltTenantAccessTokenReq) (*larkcore.TenantAccessTokenResp, error) {
			tokenFetches++
			return &larkcore.TenantAccessTokenResp{
				ApiResp: &larkcore.ApiResp{},
				CodeError: larkcore.CodeError{
					Code: 0,
				},
				Expire:            7200,
				TenantAccessToken: "cached-token",
			}, nil
		},
		writeWikiMarkdown: func(ctx context.Context, wikiURL string, markdown string, tenantAccessToken string) error {
			callTokens = append(callTokens, tenantAccessToken)
			if wikiURL == "" || markdown == "" {
				t.Fatalf("unexpected empty input")
			}
			return nil
		},
	}

	if err := client.WriteWikiMarkdown(context.Background(), "https://example.feishu.cn/wiki/abc", "# title"); err != nil {
		t.Fatalf("WriteWikiMarkdown: %v", err)
	}

	if tokenFetches != 1 {
		t.Fatalf("unexpected token fetches: %d", tokenFetches)
	}
	if len(callTokens) != 1 || callTokens[0] != "cached-token" {
		t.Fatalf("unexpected call tokens: %#v", callTokens)
	}
}

func TestListChatMessagesRefreshesInvalidTenantToken(t *testing.T) {
	now := time.Unix(400, 0)
	tokenFetches := 0
	callTokens := make([]string, 0, 2)

	client := &Client{
		appID:     "cli_xxx",
		appSecret: "secret_xxx",
		now:       func() time.Time { return now },
		getTenantAccessToken: func(ctx context.Context, req *larkcore.SelfBuiltTenantAccessTokenReq) (*larkcore.TenantAccessTokenResp, error) {
			tokenFetches++
			return &larkcore.TenantAccessTokenResp{
				ApiResp: &larkcore.ApiResp{},
				CodeError: larkcore.CodeError{
					Code: 0,
				},
				Expire:            7200,
				TenantAccessToken: "fresh-token",
			}, nil
		},
		listChatMessages: func(ctx context.Context, chatID string, endTime string, pageSize int, tenantAccessToken string) ([]ChatMessage, error) {
			callTokens = append(callTokens, tenantAccessToken)
			if tenantAccessToken == "stale-token" {
				return nil, newAPIError("list chat messages", errCodeTenantAccessTokenInvalid, "invalid", "req-stale")
			}
			return []ChatMessage{{MessageID: "om_1", ChatID: chatID}}, nil
		},
	}
	client.tenantAccessToken = "stale-token"
	client.tenantAccessTokenExpireAt = now.Add(time.Hour)

	items, err := client.ListChatMessages(context.Background(), "oc_123", "1700000000", 20)
	if err != nil {
		t.Fatalf("ListChatMessages: %v", err)
	}
	if len(items) != 1 || items[0].MessageID != "om_1" {
		t.Fatalf("unexpected items: %#v", items)
	}
	if tokenFetches != 1 {
		t.Fatalf("unexpected token fetches: %d", tokenFetches)
	}
	if len(callTokens) != 2 || callTokens[0] != "stale-token" || callTokens[1] != "fresh-token" {
		t.Fatalf("unexpected call tokens: %#v", callTokens)
	}
}

func TestPreviewMessageContent(t *testing.T) {
	if got := PreviewMessageContent("text", `{"text":"hello"}`); got != "hello" {
		t.Fatalf("unexpected text preview: %q", got)
	}
	if got := PreviewMessageContent("image", `{"image_key":"img_1"}`); got != "[image]" {
		t.Fatalf("unexpected image preview: %q", got)
	}
	if got := PreviewMessageContent("file", `{}`); got != "[file]" {
		t.Fatalf("unexpected file preview: %q", got)
	}
}

func TestResolveWikiDocumentIDFromDocxURL(t *testing.T) {
	documentID, err := resolveWikiDocumentID(context.Background(), nil, "https://example.feishu.cn/docx/AbCdEf123?from=wiki", "tenant-token")
	if err != nil {
		t.Fatalf("resolveWikiDocumentID: %v", err)
	}
	if documentID != "AbCdEf123" {
		t.Fatalf("unexpected document id: %s", documentID)
	}
}

func TestResolveWikiDocumentIDRejectsUnsupportedURL(t *testing.T) {
	_, err := resolveWikiDocumentID(context.Background(), nil, "https://example.feishu.cn/base/AbCdEf123", "tenant-token")
	if err == nil {
		t.Fatal("expected error")
	}
}
