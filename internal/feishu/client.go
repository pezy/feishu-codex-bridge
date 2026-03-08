package feishu

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const tenantAccessTokenExpiryDelta = 3 * time.Minute

const (
	errCodeAccessTokenInvalid       = 99991671
	errCodeAppAccessTokenInvalid    = 99991664
	errCodeTenantAccessTokenInvalid = 99991663
)

type Client struct {
	appID     string
	appSecret string

	now func() time.Time

	getTenantAccessToken func(context.Context, *larkcore.SelfBuiltTenantAccessTokenReq) (*larkcore.TenantAccessTokenResp, error)
	sendTextToOpenID     func(context.Context, string, string, string) (string, error)
	replyText            func(context.Context, string, string, string) (string, error)
	addReaction          func(context.Context, string, string, string) (string, error)
	deleteReaction       func(context.Context, string, string, string) error

	mu                        sync.Mutex
	tenantAccessToken         string
	tenantAccessTokenExpireAt time.Time
}

func New(appID string, appSecret string) *Client {
	sdk := lark.NewClient(appID, appSecret, lark.WithEnableTokenCache(false))
	client := &Client{
		appID:                appID,
		appSecret:            appSecret,
		now:                  time.Now,
		getTenantAccessToken: sdk.GetTenantAccessTokenBySelfBuiltApp,
	}
	client.sendTextToOpenID = func(ctx context.Context, openID string, text string, tenantAccessToken string) (string, error) {
		content := fmt.Sprintf("{\"text\":%q}", text)
		resp, err := sdk.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("open_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(openID).
				MsgType("text").
				Content(content).
				Uuid(randomUUID()).
				Build()).
			Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
		if err != nil {
			return "", fmt.Errorf("create message: %w", err)
		}
		if !resp.Success() {
			return "", newAPIError("create message", resp.Code, resp.Msg, resp.RequestId())
		}
		return value(resp.Data.MessageId), nil
	}
	client.replyText = func(ctx context.Context, messageID string, text string, tenantAccessToken string) (string, error) {
		content := fmt.Sprintf("{\"text\":%q}", text)
		resp, err := sdk.Im.V1.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
			MessageId(messageID).
			Body(larkim.NewReplyMessageReqBodyBuilder().
				MsgType("text").
				Content(content).
				ReplyInThread(false).
				Uuid(randomUUID()).
				Build()).
			Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
		if err != nil {
			return "", fmt.Errorf("reply message: %w", err)
		}
		if !resp.Success() {
			return "", newAPIError("reply message", resp.Code, resp.Msg, resp.RequestId())
		}
		return value(resp.Data.MessageId), nil
	}
	client.addReaction = func(ctx context.Context, messageID string, emojiType string, tenantAccessToken string) (string, error) {
		resp, err := sdk.Im.V1.MessageReaction.Create(ctx, larkim.NewCreateMessageReactionReqBuilder().
			MessageId(messageID).
			Body(larkim.NewCreateMessageReactionReqBodyBuilder().
				ReactionType(larkim.NewEmojiBuilder().
					EmojiType(emojiType).
					Build()).
				Build()).
			Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
		if err != nil {
			return "", fmt.Errorf("create message reaction: %w", err)
		}
		if !resp.Success() {
			return "", newAPIError("create message reaction", resp.Code, resp.Msg, resp.RequestId())
		}
		return value(resp.Data.ReactionId), nil
	}
	client.deleteReaction = func(ctx context.Context, messageID string, reactionID string, tenantAccessToken string) error {
		resp, err := sdk.Im.V1.MessageReaction.Delete(ctx, larkim.NewDeleteMessageReactionReqBuilder().
			MessageId(messageID).
			ReactionId(reactionID).
			Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
		if err != nil {
			return fmt.Errorf("delete message reaction: %w", err)
		}
		if !resp.Success() {
			return newAPIError("delete message reaction", resp.Code, resp.Msg, resp.RequestId())
		}
		return nil
	}
	return client
}

func (c *Client) SendTextToOpenID(ctx context.Context, openID string, text string) (string, error) {
	return withTenantAccessToken(ctx, c, func(token string) (string, error) {
		return c.sendTextToOpenID(ctx, openID, text, token)
	})
}

func (c *Client) ReplyText(ctx context.Context, messageID string, text string) (string, error) {
	return withTenantAccessToken(ctx, c, func(token string) (string, error) {
		return c.replyText(ctx, messageID, text, token)
	})
}

func (c *Client) AddReaction(ctx context.Context, messageID string, emojiType string) (string, error) {
	return withTenantAccessToken(ctx, c, func(token string) (string, error) {
		return c.addReaction(ctx, messageID, emojiType, token)
	})
}

func (c *Client) DeleteReaction(ctx context.Context, messageID string, reactionID string) error {
	_, err := withTenantAccessToken(ctx, c, func(token string) (struct{}, error) {
		return struct{}{}, c.deleteReaction(ctx, messageID, reactionID, token)
	})
	return err
}

func withTenantAccessToken[T any](ctx context.Context, c *Client, call func(token string) (T, error)) (T, error) {
	var zero T

	token, err := c.tenantToken(ctx, false)
	if err != nil {
		return zero, err
	}

	result, err := call(token)
	if err == nil || !isInvalidAccessTokenError(err) {
		return result, err
	}

	c.invalidateTenantToken(token)

	token, err = c.tenantToken(ctx, true)
	if err != nil {
		return zero, err
	}
	return call(token)
}

func (c *Client) tenantToken(ctx context.Context, forceRefresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !forceRefresh && c.tenantAccessToken != "" && c.now().Before(c.tenantAccessTokenExpireAt) {
		return c.tenantAccessToken, nil
	}

	resp, err := c.getTenantAccessToken(ctx, &larkcore.SelfBuiltTenantAccessTokenReq{
		AppID:     c.appID,
		AppSecret: c.appSecret,
	})
	if err != nil {
		return "", fmt.Errorf("get tenant access token: %w", err)
	}
	if !resp.Success() {
		return "", newAPIError("get tenant access token", resp.Code, resp.Msg, resp.RequestId())
	}

	c.tenantAccessToken = resp.TenantAccessToken
	c.tenantAccessTokenExpireAt = c.now().Add(tenantAccessTokenTTL(resp.Expire))
	return c.tenantAccessToken, nil
}

func (c *Client) invalidateTenantToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if token != "" && c.tenantAccessToken != token {
		return
	}
	c.tenantAccessToken = ""
	c.tenantAccessTokenExpireAt = time.Time{}
}

func tenantAccessTokenTTL(expireSeconds int) time.Duration {
	ttl := time.Duration(expireSeconds)*time.Second - tenantAccessTokenExpiryDelta
	if ttl <= 0 {
		return time.Second
	}
	return ttl
}

type apiError struct {
	operation string
	code      int
	msg       string
	requestID string
}

func newAPIError(operation string, code int, msg string, requestID string) error {
	return &apiError{
		operation: operation,
		code:      code,
		msg:       msg,
		requestID: requestID,
	}
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s failed: code=%d msg=%s request_id=%s", e.operation, e.code, e.msg, e.requestID)
}

func isInvalidAccessTokenError(err error) bool {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return false
	}

	switch apiErr.code {
	case errCodeAccessTokenInvalid, errCodeAppAccessTokenInvalid, errCodeTenantAccessTokenInvalid:
		return true
	default:
		return false
	}
}

func value(input *string) string {
	if input == nil {
		return ""
	}
	return *input
}

func randomUUID() string {
	return randomHex(16)
}
