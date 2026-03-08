package feishu

import (
	"context"
	"fmt"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type Client struct {
	sdk *lark.Client
}

func New(appID string, appSecret string) *Client {
	return &Client{
		sdk: lark.NewClient(appID, appSecret),
	}
}

func (c *Client) SendTextToOpenID(ctx context.Context, openID string, text string) (string, error) {
	content := fmt.Sprintf("{\"text\":%q}", text)
	resp, err := c.sdk.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("open_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(openID).
			MsgType("text").
			Content(content).
			Uuid(randomUUID()).
			Build()).
		Build())
	if err != nil {
		return "", fmt.Errorf("create message: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("create message failed: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
	}
	return value(resp.Data.MessageId), nil
}

func (c *Client) ReplyText(ctx context.Context, messageID string, text string) (string, error) {
	content := fmt.Sprintf("{\"text\":%q}", text)
	resp, err := c.sdk.Im.V1.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			MsgType("text").
			Content(content).
			ReplyInThread(false).
			Uuid(randomUUID()).
			Build()).
		Build())
	if err != nil {
		return "", fmt.Errorf("reply message: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("reply message failed: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
	}
	return value(resp.Data.MessageId), nil
}

func (c *Client) AddReaction(ctx context.Context, messageID string, emojiType string) (string, error) {
	resp, err := c.sdk.Im.V1.MessageReaction.Create(ctx, larkim.NewCreateMessageReactionReqBuilder().
		MessageId(messageID).
		Body(larkim.NewCreateMessageReactionReqBodyBuilder().
			ReactionType(larkim.NewEmojiBuilder().
				EmojiType(emojiType).
				Build()).
			Build()).
		Build())
	if err != nil {
		return "", fmt.Errorf("create message reaction: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("create message reaction failed: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
	}
	return value(resp.Data.ReactionId), nil
}

func (c *Client) DeleteReaction(ctx context.Context, messageID string, reactionID string) error {
	resp, err := c.sdk.Im.V1.MessageReaction.Delete(ctx, larkim.NewDeleteMessageReactionReqBuilder().
		MessageId(messageID).
		ReactionId(reactionID).
		Build())
	if err != nil {
		return fmt.Errorf("delete message reaction: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("delete message reaction failed: code=%d msg=%s request_id=%s", resp.Code, resp.Msg, resp.RequestId())
	}
	return nil
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
