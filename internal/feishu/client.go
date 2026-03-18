package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkapplication "github.com/larksuite/oapi-sdk-go/v3/service/application/v6"
	larkdocx "github.com/larksuite/oapi-sdk-go/v3/service/docx/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkwiki "github.com/larksuite/oapi-sdk-go/v3/service/wiki/v2"
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
	sendImageToOpenID    func(context.Context, string, string, string) (string, error)
	replyText            func(context.Context, string, string, string) (string, error)
	replyImage           func(context.Context, string, string, string) (string, error)
	listChatMessages     func(context.Context, string, string, int, string) ([]ChatMessage, error)
	addReaction          func(context.Context, string, string, string) (string, error)
	deleteReaction       func(context.Context, string, string, string) error
	uploadImage          func(context.Context, string, string) (string, error)
	downloadMessageImage func(context.Context, string, string, string) (messageImage, error)
	getBotMentionNames   func(context.Context, string) ([]string, error)
	writeWikiMarkdown    func(context.Context, string, string, string) error

	mu                        sync.Mutex
	tenantAccessToken         string
	tenantAccessTokenExpireAt time.Time
}

type messageImage struct {
	FileName string
	Data     []byte
}

type MessageImage = messageImage

type ChatMessage struct {
	MessageID  string
	ChatID     string
	ThreadID   string
	ParentID   string
	SenderID   string
	SenderType string
	SenderRole string
	MsgType    string
	Content    string
	CreatedAt  time.Time
	Deleted    bool
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
	client.uploadImage = func(ctx context.Context, imagePath string, tenantAccessToken string) (string, error) {
		body, err := larkim.NewCreateImagePathReqBodyBuilder().
			ImageType("message").
			ImagePath(imagePath).
			Build()
		if err != nil {
			return "", fmt.Errorf("build create image body: %w", err)
		}
		resp, err := sdk.Im.V1.Image.Create(ctx, larkim.NewCreateImageReqBuilder().
			Body(body).
			Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
		if err != nil {
			return "", fmt.Errorf("upload image: %w", err)
		}
		if !resp.Success() {
			return "", newAPIError("upload image", resp.Code, resp.Msg, resp.RequestId())
		}
		return value(resp.Data.ImageKey), nil
	}
	client.sendImageToOpenID = func(ctx context.Context, openID string, imagePath string, tenantAccessToken string) (string, error) {
		imageKey, err := client.uploadImage(ctx, imagePath, tenantAccessToken)
		if err != nil {
			return "", err
		}
		resp, err := sdk.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("open_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(openID).
				MsgType("image").
				Content(fmt.Sprintf("{\"image_key\":%q}", imageKey)).
				Uuid(randomUUID()).
				Build()).
			Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
		if err != nil {
			return "", fmt.Errorf("create image message: %w", err)
		}
		if !resp.Success() {
			return "", newAPIError("create image message", resp.Code, resp.Msg, resp.RequestId())
		}
		return value(resp.Data.MessageId), nil
	}
	client.replyImage = func(ctx context.Context, messageID string, imagePath string, tenantAccessToken string) (string, error) {
		imageKey, err := client.uploadImage(ctx, imagePath, tenantAccessToken)
		if err != nil {
			return "", err
		}
		resp, err := sdk.Im.V1.Message.Reply(ctx, larkim.NewReplyMessageReqBuilder().
			MessageId(messageID).
			Body(larkim.NewReplyMessageReqBodyBuilder().
				MsgType("image").
				Content(fmt.Sprintf("{\"image_key\":%q}", imageKey)).
				ReplyInThread(false).
				Uuid(randomUUID()).
				Build()).
			Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
		if err != nil {
			return "", fmt.Errorf("reply image message: %w", err)
		}
		if !resp.Success() {
			return "", newAPIError("reply image message", resp.Code, resp.Msg, resp.RequestId())
		}
		return value(resp.Data.MessageId), nil
	}
	client.listChatMessages = func(ctx context.Context, chatID string, endTime string, pageSize int, tenantAccessToken string) ([]ChatMessage, error) {
		if pageSize <= 0 {
			pageSize = 20
		}
		builder := larkim.NewListMessageReqBuilder().
			ContainerIdType("chat").
			ContainerId(chatID).
			SortType(larkim.SortTypeListMessageByCreateTimeDesc).
			PageSize(pageSize)
		if strings.TrimSpace(endTime) != "" {
			builder.EndTime(endTime)
		}
		resp, err := sdk.Im.V1.Message.List(ctx, builder.Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
		if err != nil {
			return nil, fmt.Errorf("list chat messages: %w", err)
		}
		if !resp.Success() {
			return nil, newAPIError("list chat messages", resp.Code, resp.Msg, resp.RequestId())
		}
		if resp.Data == nil {
			return nil, nil
		}

		items := make([]ChatMessage, 0, len(resp.Data.Items))
		for _, item := range resp.Data.Items {
			if item == nil {
				continue
			}
			items = append(items, toChatMessage(item))
		}
		return items, nil
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
	client.downloadMessageImage = func(ctx context.Context, messageID string, imageKey string, tenantAccessToken string) (messageImage, error) {
		resp, err := sdk.Im.V1.MessageResource.Get(ctx, larkim.NewGetMessageResourceReqBuilder().
			MessageId(messageID).
			FileKey(imageKey).
			Type("image").
			Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
		if err != nil {
			return messageImage{}, fmt.Errorf("download message image: %w", err)
		}
		if !resp.Success() && resp.File == nil {
			return messageImage{}, newAPIError("download message image", resp.Code, resp.Msg, resp.RequestId())
		}
		data, err := io.ReadAll(resp.File)
		if err != nil {
			return messageImage{}, fmt.Errorf("read message image: %w", err)
		}
		return messageImage{FileName: resp.FileName, Data: data}, nil
	}
	client.getBotMentionNames = func(ctx context.Context, tenantAccessToken string) ([]string, error) {
		resp, err := sdk.Application.V6.Application.Get(ctx, larkapplication.NewGetApplicationReqBuilder().
			AppId("me").
			Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
		if err != nil {
			return nil, fmt.Errorf("get application info: %w", err)
		}
		if !resp.Success() {
			return nil, newAPIError("get application info", resp.Code, resp.Msg, resp.RequestId())
		}
		if resp.Data == nil {
			return nil, nil
		}
		return collectBotMentionNames(resp.Data.App), nil
	}
	client.writeWikiMarkdown = func(ctx context.Context, wikiURL string, markdown string, tenantAccessToken string) error {
		documentID, err := resolveWikiDocumentID(ctx, sdk, wikiURL, tenantAccessToken)
		if err != nil {
			return err
		}
		return replaceDocumentMarkdown(ctx, sdk, documentID, markdown, tenantAccessToken)
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

func (c *Client) SendImageToOpenID(ctx context.Context, openID string, imagePath string) (string, error) {
	return withTenantAccessToken(ctx, c, func(token string) (string, error) {
		return c.sendImageToOpenID(ctx, openID, imagePath, token)
	})
}

func (c *Client) ReplyImage(ctx context.Context, messageID string, imagePath string) (string, error) {
	return withTenantAccessToken(ctx, c, func(token string) (string, error) {
		return c.replyImage(ctx, messageID, imagePath, token)
	})
}

func (c *Client) AddReaction(ctx context.Context, messageID string, emojiType string) (string, error) {
	return withTenantAccessToken(ctx, c, func(token string) (string, error) {
		return c.addReaction(ctx, messageID, emojiType, token)
	})
}

func (c *Client) ListChatMessages(ctx context.Context, chatID string, endTime string, pageSize int) ([]ChatMessage, error) {
	return withTenantAccessToken(ctx, c, func(token string) ([]ChatMessage, error) {
		return c.listChatMessages(ctx, chatID, endTime, pageSize, token)
	})
}

func (c *Client) GetBotMentionNames(ctx context.Context) ([]string, error) {
	return withTenantAccessToken(ctx, c, func(token string) ([]string, error) {
		return c.getBotMentionNames(ctx, token)
	})
}

func (c *Client) DeleteReaction(ctx context.Context, messageID string, reactionID string) error {
	_, err := withTenantAccessToken(ctx, c, func(token string) (struct{}, error) {
		return struct{}{}, c.deleteReaction(ctx, messageID, reactionID, token)
	})
	return err
}

func (c *Client) DownloadMessageImageToPath(ctx context.Context, messageID string, imageKey string, destPath string) error {
	image, err := c.DownloadMessageImage(ctx, messageID, imageKey)
	if err != nil {
		return err
	}
	if err := os.WriteFile(destPath, image.Data, 0o644); err != nil {
		return fmt.Errorf("write message image: %w", err)
	}
	return nil
}

func (c *Client) DownloadMessageImage(ctx context.Context, messageID string, imageKey string) (messageImage, error) {
	return withTenantAccessToken(ctx, c, func(token string) (messageImage, error) {
		return c.downloadMessageImage(ctx, messageID, imageKey, token)
	})
}

func (c *Client) WriteWikiMarkdown(ctx context.Context, wikiURL string, markdown string) error {
	_, err := withTenantAccessToken(ctx, c, func(token string) (struct{}, error) {
		return struct{}{}, c.writeWikiMarkdown(ctx, wikiURL, markdown, token)
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

func collectBotMentionNames(app *larkapplication.Application) []string {
	names := make([]string, 0, 1)
	seen := make(map[string]struct{})
	appendName := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	if app == nil {
		return names
	}

	appendName(value(app.AppName))
	for _, item := range app.I18n {
		if item == nil {
			continue
		}
		appendName(value(item.Name))
	}
	return names
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

func toChatMessage(message *larkim.Message) ChatMessage {
	msg := ChatMessage{
		MessageID: value(message.MessageId),
		ChatID:    value(message.ChatId),
		ThreadID:  value(message.ThreadId),
		ParentID:  value(message.ParentId),
		MsgType:   value(message.MsgType),
		CreatedAt: parseMessageTimestamp(value(message.CreateTime)),
		Deleted:   message.Deleted != nil && *message.Deleted,
	}
	if message.Sender != nil {
		msg.SenderID = value(message.Sender.Id)
		msg.SenderType = value(message.Sender.SenderType)
		msg.SenderRole = value(message.Sender.IdType)
	}
	if message.Body != nil {
		msg.Content = value(message.Body.Content)
	}
	return msg
}

func parseMessageTimestamp(input string) time.Time {
	input = strings.TrimSpace(input)
	if input == "" {
		return time.Time{}
	}

	switch len(input) {
	case 13:
		var ms int64
		if _, err := fmt.Sscanf(input, "%d", &ms); err == nil {
			return time.UnixMilli(ms).UTC()
		}
	case 10:
		var sec int64
		if _, err := fmt.Sscanf(input, "%d", &sec); err == nil {
			return time.Unix(sec, 0).UTC()
		}
	}

	var sec int64
	if _, err := fmt.Sscanf(input, "%d", &sec); err == nil {
		return time.Unix(sec, 0).UTC()
	}
	return time.Time{}
}

func PreviewMessageContent(msgType string, rawContent string) string {
	msgType = strings.TrimSpace(msgType)
	rawContent = strings.TrimSpace(rawContent)

	switch msgType {
	case "text":
		var body struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(rawContent), &body); err == nil && strings.TrimSpace(body.Text) != "" {
			return strings.TrimSpace(body.Text)
		}
	case "image":
		return "[image]"
	}

	if msgType == "" {
		return ""
	}
	return fmt.Sprintf("[%s]", msgType)
}

func resolveWikiDocumentID(ctx context.Context, sdk *lark.Client, wikiURL string, tenantAccessToken string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(wikiURL))
	if err != nil {
		return "", fmt.Errorf("parse wiki url: %w", err)
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i < len(segments)-1; i++ {
		switch segments[i] {
		case "docx":
			return segments[i+1], nil
		case "wiki":
			resp, err := sdk.Wiki.V2.Space.GetNode(ctx, larkwiki.NewGetNodeSpaceReqBuilder().
				Token(segments[i+1]).
				ObjType(larkwiki.ObjTypeForQueryObjTypeWiki).
				Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
			if err != nil {
				return "", fmt.Errorf("get wiki node: %w", err)
			}
			if !resp.Success() {
				return "", newAPIError("get wiki node", resp.Code, resp.Msg, resp.RequestId())
			}
			if resp.Data == nil || resp.Data.Node == nil {
				return "", errors.New("get wiki node: empty node")
			}
			if value(resp.Data.Node.ObjType) != larkwiki.ObjTypeForQueryObjTypeDocx {
				return "", fmt.Errorf("unsupported wiki node obj_type: %s", value(resp.Data.Node.ObjType))
			}
			documentID := value(resp.Data.Node.ObjToken)
			if documentID == "" {
				return "", errors.New("get wiki node: empty obj_token")
			}
			return documentID, nil
		}
	}
	return "", fmt.Errorf("unsupported wiki url: %s", wikiURL)
}

func replaceDocumentMarkdown(ctx context.Context, sdk *lark.Client, documentID string, markdown string, tenantAccessToken string) error {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return errors.New("markdown is required")
	}

	convertResp, err := sdk.Docx.V1.Document.Convert(ctx, larkdocx.NewConvertDocumentReqBuilder().
		Body(larkdocx.NewConvertDocumentReqBodyBuilder().
			ContentType(larkdocx.ContentTypeMarkdown).
			Content(markdown).
			Build()).
		Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
	if err != nil {
		return fmt.Errorf("convert markdown: %w", err)
	}
	if !convertResp.Success() {
		return newAPIError("convert markdown", convertResp.Code, convertResp.Msg, convertResp.RequestId())
	}

	childrenResp, err := sdk.Docx.V1.DocumentBlockChildren.Get(ctx, larkdocx.NewGetDocumentBlockChildrenReqBuilder().
		DocumentId(documentID).
		BlockId(documentID).
		PageSize(500).
		Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
	if err != nil {
		return fmt.Errorf("get document root children: %w", err)
	}
	if !childrenResp.Success() {
		return newAPIError("get document root children", childrenResp.Code, childrenResp.Msg, childrenResp.RequestId())
	}
	if childrenResp.Data != nil && len(childrenResp.Data.Items) > 0 {
		deleteResp, err := sdk.Docx.V1.DocumentBlockChildren.BatchDelete(ctx, larkdocx.NewBatchDeleteDocumentBlockChildrenReqBuilder().
			DocumentId(documentID).
			BlockId(documentID).
			Body(larkdocx.NewBatchDeleteDocumentBlockChildrenReqBodyBuilder().
				StartIndex(0).
				EndIndex(len(childrenResp.Data.Items)).
				Build()).
			Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
		if err != nil {
			return fmt.Errorf("delete document root children: %w", err)
		}
		if !deleteResp.Success() {
			return newAPIError("delete document root children", deleteResp.Code, deleteResp.Msg, deleteResp.RequestId())
		}
	}

	blocks := convertResp.Data.Blocks
	if len(blocks) == 0 {
		return nil
	}
	createResp, err := sdk.Docx.V1.DocumentBlockChildren.Create(ctx, larkdocx.NewCreateDocumentBlockChildrenReqBuilder().
		DocumentId(documentID).
		BlockId(documentID).
		Body(larkdocx.NewCreateDocumentBlockChildrenReqBodyBuilder().
			Children(blocks).
			Index(0).
			Build()).
		Build(), larkcore.WithTenantAccessToken(tenantAccessToken))
	if err != nil {
		return fmt.Errorf("create document root children: %w", err)
	}
	if !createResp.Success() {
		return newAPIError("create document root children", createResp.Code, createResp.Msg, createResp.RequestId())
	}
	return nil
}

func randomUUID() string {
	return randomHex(16)
}
