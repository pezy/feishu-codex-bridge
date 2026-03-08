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
