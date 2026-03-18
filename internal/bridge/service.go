package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pezy/feishu-codex-bridge/internal/codex"
	"github.com/pezy/feishu-codex-bridge/internal/config"
	"github.com/pezy/feishu-codex-bridge/internal/feishu"
	"github.com/pezy/feishu-codex-bridge/internal/prompt"
	"github.com/pezy/feishu-codex-bridge/internal/store"
)

const (
	pairingCommand               = "/pair"
	noReplyFallback              = "任务已执行，但没有可发送的回复。请查看本机日志。"
	groupImageUnsupportedMessage = "已生成图片，但当前群聊回复仅支持文本。"
	wikiWriteSuccessMessage      = "已写入飞书 Wiki 页面。"
	imageMarkerPrefix            = "[[image:"
	imageMarkerSuffix            = "]]"
	wikiWriteBeginPrefix         = "[[wiki-write:"
	wikiWriteEndMarker           = "[[/wiki-write]]"
)

type Status struct {
	Service            string             `json:"service"`
	HTTPAddr           string             `json:"http_addr"`
	DefaultWorkDir     string             `json:"default_work_dir"`
	AuthorizedOpenID   string             `json:"authorized_open_id"`
	WsRunning          bool               `json:"ws_running"`
	WsConnected        bool               `json:"ws_connected"`
	LastConnectedAt    *time.Time         `json:"last_connected_at,omitempty"`
	LastEventAt        *time.Time         `json:"last_event_at,omitempty"`
	LastError          string             `json:"last_error,omitempty"`
	LastExecution      *ExecutionSnapshot `json:"last_execution,omitempty"`
	RecentContextLimit int                `json:"recent_context_limit"`
}

type ExecutionSnapshot struct {
	RequestMessageID string     `json:"request_message_id"`
	Status           string     `json:"status"`
	ErrorText        string     `json:"error_text,omitempty"`
	StartedAt        time.Time  `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

type Service struct {
	cfg    config.Config
	store  *store.SQLiteStore
	feishu feishuClient
	runner *codex.Runner

	mu          sync.RWMutex
	wsRunning   bool
	wsConnected bool
	lastError   string
	lastEventAt *time.Time
	lastConnAt  *time.Time
	lastExec    *ExecutionSnapshot

	botMentionNames       []string
	botMentionNamesLoaded bool
}

type feishuClient interface {
	SendTextToOpenID(context.Context, string, string) (string, error)
	SendImageToOpenID(context.Context, string, string) (string, error)
	ReplyText(context.Context, string, string) (string, error)
	ReplyImage(context.Context, string, string) (string, error)
	AddReaction(context.Context, string, string) (string, error)
	DeleteReaction(context.Context, string, string) error
	DownloadMessageImage(context.Context, string, string) (feishu.MessageImage, error)
	WriteWikiMarkdown(context.Context, string, string) error
	ListChatMessages(context.Context, string, string, int) ([]feishu.ChatMessage, error)
	GetBotMentionNames(context.Context) ([]string, error)
}

type incomingMessage struct {
	Message      *larkim.EventMessage
	MessageID    string
	ChatID       string
	ChatType     string
	MessageType  string
	SenderOpenID string
	Text         string
	ImageKey     string
	RawContent   string
	HasMentions  bool
	Mentions     []*larkim.MentionEvent
}

type responsePayload struct {
	Text       string
	ImagePaths []string
	WikiWrites []wikiWriteAction
}

type wikiWriteAction struct {
	URL      string
	Markdown string
}

func New(cfg config.Config, sqliteStore *store.SQLiteStore, feishuClient *feishu.Client, runner *codex.Runner) *Service {
	return &Service{
		cfg:    cfg,
		store:  sqliteStore,
		feishu: feishuClient,
		runner: runner,
	}
}

func (s *Service) MarkWSRunning() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wsRunning = true
}

func (s *Service) MarkWSConnected() {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wsConnected = true
	s.lastConnAt = &now
	s.lastError = ""
}

func (s *Service) MarkWSError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wsConnected = false
	if err != nil {
		s.lastError = err.Error()
	}
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	lastExecution, err := s.store.LastExecution(ctx)
	if err != nil {
		return Status{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	status := Status{
		Service:            "feishu-codex-bridge",
		HTTPAddr:           s.cfg.HTTPAddr,
		DefaultWorkDir:     s.cfg.DefaultWorkDir,
		AuthorizedOpenID:   maskOpenID(s.cfg.AuthorizedOpenID),
		WsRunning:          s.wsRunning,
		WsConnected:        s.wsConnected,
		LastError:          s.lastError,
		LastConnectedAt:    cloneTime(s.lastConnAt),
		LastEventAt:        cloneTime(s.lastEventAt),
		RecentContextLimit: s.cfg.RecentContextLimit,
	}

	if s.lastExec != nil {
		exec := *s.lastExec
		status.LastExecution = &exec
	} else if lastExecution != nil {
		status.LastExecution = &ExecutionSnapshot{
			RequestMessageID: lastExecution.RequestMessageID,
			Status:           lastExecution.Status,
			ErrorText:        lastExecution.ErrorText,
			StartedAt:        lastExecution.StartedAt,
			CompletedAt:      lastExecution.CompletedAt,
		}
	}

	return status, nil
}

func (s *Service) RecentConversations(ctx context.Context, limit int) ([]store.ConversationEntry, error) {
	if limit <= 0 {
		limit = s.cfg.RecentContextLimit
	}
	return s.store.RecentConversations(ctx, limit)
}

func (s *Service) SendBoundMessage(ctx context.Context, text string, imagePaths []string) ([]string, error) {
	if strings.TrimSpace(text) == "" && len(imagePaths) == 0 {
		return nil, errors.New("text or image_paths is required")
	}

	var messageIDs []string
	if strings.TrimSpace(text) != "" {
		id, err := s.feishu.SendTextToOpenID(ctx, s.cfg.AuthorizedOpenID, text)
		if err != nil {
			return nil, err
		}
		messageIDs = append(messageIDs, id)
		if err := s.store.AppendConversation(ctx, store.ConversationEntry{
			Source:      "assistant",
			OpenID:      s.cfg.AuthorizedOpenID,
			MessageID:   id,
			Content:     text,
			ContentType: "text",
			CreatedAt:   time.Now().UTC(),
		}); err != nil {
			log.Printf("append outbound text conversation: %v", err)
		}
	}

	for _, imagePath := range imagePaths {
		id, err := s.feishu.SendImageToOpenID(ctx, s.cfg.AuthorizedOpenID, imagePath)
		if err != nil {
			return nil, err
		}
		messageIDs = append(messageIDs, id)
		if err := s.store.AppendConversation(ctx, store.ConversationEntry{
			Source:      "assistant",
			OpenID:      s.cfg.AuthorizedOpenID,
			MessageID:   id,
			Content:     "[image]",
			ContentType: "image",
			FilePath:    imagePath,
			CreatedAt:   time.Now().UTC(),
		}); err != nil {
			log.Printf("append outbound image conversation: %v", err)
		}
	}

	return messageIDs, nil
}

func (s *Service) ListPendingPairingRequests(ctx context.Context) ([]store.PairingRequest, error) {
	return s.store.ListPendingPairingRequests(ctx)
}

func (s *Service) ApprovePairingRequest(ctx context.Context, openID string) error {
	if err := s.store.SetPairingRequestStatus(ctx, openID, "approved"); err != nil {
		return err
	}
	return s.store.EnsureAuthorizedUser(ctx, openID)
}

func (s *Service) RejectPairingRequest(ctx context.Context, openID string) error {
	return s.store.SetPairingRequestStatus(ctx, openID, "rejected")
}

func (s *Service) AuthorizeGroup(ctx context.Context, chatID string) error {
	return s.store.EnsureAuthorizedGroup(ctx, chatID)
}

func (s *Service) RemoveAuthorizedGroup(ctx context.Context, chatID string) error {
	return s.store.RemoveAuthorizedGroup(ctx, chatID)
}

func (s *Service) HandleIncomingMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	incoming, err := parseIncoming(event)
	if err != nil {
		if isIgnoredEvent(err) {
			return nil
		}
		return err
	}

	now := time.Now().UTC()
	s.markEvent(now)

	shouldTrigger := true
	switch incoming.ChatType {
	case "p2p":
		authorized, err := s.store.IsAuthorizedUser(ctx, incoming.SenderOpenID)
		if err != nil {
			return err
		}
		if !authorized {
			return s.handleUnauthorizedP2P(ctx, incoming)
		}
	case "group":
		authorized, err := s.store.IsAuthorizedGroup(ctx, incoming.ChatID)
		if err != nil {
			return err
		}
		if !authorized {
			if err := s.store.EnsureAuthorizedGroup(ctx, incoming.ChatID); err != nil {
				return err
			}
		}
		if incoming.MessageType != "text" {
			return nil
		}
		shouldTrigger = s.shouldTriggerGroupMessage(ctx, incoming)
	default:
		return nil
	}

	inserted, err := s.store.TryCreateInbound(ctx, store.MessageRecord{
		MessageID:      incoming.MessageID,
		ChatID:         incoming.ChatID,
		ChatType:       incoming.ChatType,
		MessageType:    incoming.MessageType,
		SenderOpenID:   incoming.SenderOpenID,
		TextContent:    incoming.Text,
		RawContentJSON: incoming.RawContent,
		Status:         "received",
		CreatedAt:      now,
	})
	if err != nil {
		return err
	}
	if !inserted {
		return nil
	}

	// Only build prompt entry when triggering execution (not for passive group context)
	var currentPromptMessage string
	if shouldTrigger {
		_, currentPromptMessage, err = s.prepareIncomingConversation(ctx, incoming, now)
		if err != nil {
			return err
		}
	}

	if !shouldTrigger {
		if err := s.store.UpdateMessageState(ctx, incoming.MessageID, "recorded", "", "", "", "", &now); err != nil {
			log.Printf("update passive context message state: %v", err)
		}
		return nil
	}

	ackReactionID, ackErr := s.feishu.AddReaction(ctx, incoming.MessageID, s.cfg.AckReactionType)
	if ackErr != nil {
		log.Printf("send ack reaction failed: %v", ackErr)
	}

	executionID := randomID("exec")
	history, err := s.buildPromptHistory(ctx, incoming)
	if err != nil {
		return err
	}
	finalPrompt := prompt.Build(s.cfg.DefaultWorkDir, excludeMessage(history, incoming.MessageID), currentPromptMessage)

	execution := store.ExecutionRecord{
		ID:               executionID,
		RequestMessageID: incoming.MessageID,
		Prompt:           finalPrompt,
		Status:           "running",
		StartedAt:        time.Now().UTC(),
	}
	if err := s.store.CreateExecution(ctx, execution); err != nil {
		return err
	}
	if err := s.store.UpdateMessageState(ctx, incoming.MessageID, "processing", "", "", executionID, "", nil); err != nil {
		return err
	}
	s.markExecution(&ExecutionSnapshot{
		RequestMessageID: incoming.MessageID,
		Status:           "running",
		StartedAt:        execution.StartedAt,
	})

	result, runErr := s.runner.Run(ctx, finalPrompt)
	completedAt := time.Now().UTC()
	responseText := strings.TrimSpace(result.Output)
	if responseText == "" {
		responseText = "任务已执行，但没有返回可发送文本。请查看本机日志。"
	}
	if runErr != nil {
		responseText = "Codex 执行失败：\n" + responseText
	}

	payload := parseResponsePayload(responseText)
	messageIDs, replyErrs := s.replyPayload(ctx, incoming, payload, completedAt)
	if ackReactionID != "" {
		if err := s.feishu.DeleteReaction(ctx, incoming.MessageID, ackReactionID); err != nil {
			log.Printf("delete ack reaction failed: %v", err)
		}
	}

	execution.Output = responseText
	execution.Status = "completed"
	execution.CompletedAt = &completedAt
	if runErr != nil {
		execution.Status = "failed"
		execution.ErrorText = runErr.Error()
	}
	if err := s.store.FinishExecution(ctx, execution); err != nil {
		log.Printf("finish execution: %v", err)
	}

	messageStatus := "completed"
	errorText := joinErrors(replyErrs)
	if runErr != nil {
		messageStatus = "failed"
		errorText = joinNonEmpty(errorText, runErr.Error())
	}
	if len(replyErrs) > 0 {
		messageStatus = "reply_failed"
	}
	if err := s.store.UpdateMessageState(
		ctx,
		incoming.MessageID,
		messageStatus,
		"",
		firstMessageID(messageIDs),
		executionID,
		errorText,
		&completedAt,
	); err != nil {
		log.Printf("update message state: %v", err)
	}

	s.markExecution(&ExecutionSnapshot{
		RequestMessageID: incoming.MessageID,
		Status:           messageStatus,
		ErrorText:        errorText,
		StartedAt:        execution.StartedAt,
		CompletedAt:      &completedAt,
	})

	return nil
}

func (s *Service) handleUnauthorizedP2P(ctx context.Context, incoming *incomingMessage) error {
	if incoming.MessageType != "text" || strings.TrimSpace(incoming.Text) != pairingCommand {
		log.Printf("ignore unauthorized sender open_id=%s", incoming.SenderOpenID)
		return nil
	}
	if err := s.store.CreateOrRefreshPairingRequest(ctx, incoming.SenderOpenID); err != nil {
		return err
	}
	if _, err := s.replyWithRetry(ctx, incoming.MessageID, pairingApprovalReplyText(s.cfg.HTTPAddr, incoming.SenderOpenID)); err != nil {
		log.Printf("send pairing reply failed: %v", err)
	}
	return nil
}

func (s *Service) prepareIncomingConversation(ctx context.Context, incoming *incomingMessage, createdAt time.Time) (store.ConversationEntry, string, error) {
	switch incoming.MessageType {
	case "image":
		imagePath, err := s.downloadIncomingImage(ctx, incoming)
		if err != nil {
			return store.ConversationEntry{}, "", err
		}
		entry := store.ConversationEntry{
			Source:      "user",
			OpenID:      incoming.SenderOpenID,
			ChatID:      incoming.ChatID,
			ChatType:    incoming.ChatType,
			MessageID:   incoming.MessageID,
			Content:     "[image]",
			ContentType: "image",
			FilePath:    imagePath,
			CreatedAt:   createdAt,
		}
		return entry, fmt.Sprintf("[image] %s", imagePath), nil
	default:
		entry := store.ConversationEntry{
			Source:      "user",
			OpenID:      incoming.SenderOpenID,
			ChatID:      incoming.ChatID,
			ChatType:    incoming.ChatType,
			MessageID:   incoming.MessageID,
			Content:     incoming.Text,
			ContentType: "text",
			CreatedAt:   createdAt,
		}
		return entry, incoming.Text, nil
	}
}

func (s *Service) buildPromptHistory(ctx context.Context, incoming *incomingMessage) ([]store.ConversationEntry, error) {
	localHistory, err := s.store.RecentConversationsByChat(ctx, incoming.ChatID, s.cfg.RecentContextLimit)
	if err != nil {
		return nil, err
	}

	if incoming.ChatType != "group" || !incoming.HasMentions {
		return localHistory, nil
	}

	remoteHistory, err := s.fetchGroupHistory(ctx, incoming)
	if err != nil {
		log.Printf("fetch remote group history failed, fallback to local: %v", err)
		return localHistory, nil
	}
	if len(remoteHistory) == 0 {
		return localHistory, nil
	}
	return remoteHistory, nil
}

func (s *Service) fetchGroupHistory(ctx context.Context, incoming *incomingMessage) ([]store.ConversationEntry, error) {
	endTime := messageEndTimeSeconds(incoming.Message)
	pageSize := s.cfg.RecentContextLimit + 8
	if pageSize < 20 {
		pageSize = 20
	}

	items, err := s.feishu.ListChatMessages(ctx, incoming.ChatID, endTime, pageSize)
	if err != nil {
		return nil, err
	}

	history := make([]store.ConversationEntry, 0, s.cfg.RecentContextLimit)
	for _, item := range items {
		if item.MessageID == incoming.MessageID || item.Deleted {
			continue
		}
		entry, ok := conversationEntryFromChatMessage(item)
		if !ok {
			continue
		}
		history = append(history, entry)
		if len(history) == s.cfg.RecentContextLimit {
			break
		}
	}
	reverseConversationEntries(history)
	return history, nil
}

func (s *Service) downloadIncomingImage(ctx context.Context, incoming *incomingMessage) (string, error) {
	image, err := s.feishu.DownloadMessageImage(ctx, incoming.MessageID, incoming.ImageKey)
	if err != nil {
		return "", err
	}

	dir := filepath.Join(s.cfg.AppSupportDir, "images", incoming.MessageID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create image dir: %w", err)
	}

	ext := imageExtension(image.FileName, image.Data)
	imagePath := filepath.Join(dir, "input"+ext)
	if err := os.WriteFile(imagePath, image.Data, 0o644); err != nil {
		return "", fmt.Errorf("write incoming image: %w", err)
	}
	return imagePath, nil
}

func conversationEntryFromChatMessage(message feishu.ChatMessage) (store.ConversationEntry, bool) {
	content := feishu.PreviewMessageContent(message.MsgType, message.Content)
	if strings.TrimSpace(content) == "" {
		return store.ConversationEntry{}, false
	}

	source := "user"
	openID := ""
	if message.SenderType == "app" {
		source = "assistant"
	} else if message.SenderRole == "open_id" {
		openID = message.SenderID
	}

	createdAt := message.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	return store.ConversationEntry{
		Source:      source,
		OpenID:      openID,
		ChatID:      message.ChatID,
		ChatType:    "group",
		MessageID:   message.MessageID,
		Content:     content,
		ContentType: "text",
		CreatedAt:   createdAt,
	}, true
}

func (s *Service) replyPayload(ctx context.Context, incoming *incomingMessage, payload responsePayload, createdAt time.Time) ([]string, []error) {
	var messageIDs []string
	var errs []error
	successfulWikiWrites := 0

	for _, action := range payload.WikiWrites {
		if strings.TrimSpace(action.URL) == "" || strings.TrimSpace(action.Markdown) == "" {
			continue
		}
		if err := s.feishu.WriteWikiMarkdown(ctx, action.URL, action.Markdown); err != nil {
			log.Printf("write wiki failed: %v", err)
			errs = append(errs, err)
			continue
		}
		successfulWikiWrites++
	}

	if payload.Text == "" && successfulWikiWrites > 0 {
		payload.Text = wikiWriteSuccessMessage
	}

	if payload.Text != "" {
		id, err := s.replyWithRetry(ctx, incoming.MessageID, payload.Text)
		if err != nil {
			log.Printf("send final reply failed: %v", err)
			errs = append(errs, err)
		} else {
			messageIDs = append(messageIDs, id)
			if err := s.store.AppendConversation(ctx, store.ConversationEntry{
				Source:      "assistant",
				OpenID:      incoming.SenderOpenID,
				ChatID:      incoming.ChatID,
				ChatType:    incoming.ChatType,
				MessageID:   id,
				Content:     payload.Text,
				ContentType: "text",
				CreatedAt:   createdAt,
			}); err != nil {
				log.Printf("append final text conversation: %v", err)
			}
		}
	}

	if incoming.ChatType == "p2p" {
		for _, imagePath := range payload.ImagePaths {
			id, err := s.replyImageWithRetry(ctx, incoming.MessageID, imagePath)
			if err != nil {
				log.Printf("send image reply failed: %v", err)
				errs = append(errs, err)
				continue
			}
			messageIDs = append(messageIDs, id)
			if err := s.store.AppendConversation(ctx, store.ConversationEntry{
				Source:      "assistant",
				OpenID:      incoming.SenderOpenID,
				ChatID:      incoming.ChatID,
				ChatType:    incoming.ChatType,
				MessageID:   id,
				Content:     "[image]",
				ContentType: "image",
				FilePath:    imagePath,
				CreatedAt:   createdAt,
			}); err != nil {
				log.Printf("append final image conversation: %v", err)
			}
		}
	} else if len(payload.ImagePaths) > 0 && payload.Text == "" {
		payload.Text = groupImageUnsupportedMessage
	}

	if len(messageIDs) > 0 {
		return messageIDs, errs
	}

	fallback := payload.Text
	if fallback == "" {
		if incoming.ChatType != "p2p" && len(payload.ImagePaths) > 0 {
			fallback = groupImageUnsupportedMessage
		} else {
			fallback = noReplyFallback
		}
	}

	id, err := s.replyWithRetry(ctx, incoming.MessageID, fallback)
	if err != nil {
		log.Printf("send fallback reply failed: %v", err)
		errs = append(errs, err)
		return nil, errs
	}
	if err := s.store.AppendConversation(ctx, store.ConversationEntry{
		Source:      "assistant",
		OpenID:      incoming.SenderOpenID,
		ChatID:      incoming.ChatID,
		ChatType:    incoming.ChatType,
		MessageID:   id,
		Content:     fallback,
		ContentType: "text",
		CreatedAt:   createdAt,
	}); err != nil {
		log.Printf("append fallback conversation: %v", err)
	}
	return []string{id}, errs
}

func (s *Service) replyWithRetry(ctx context.Context, messageID string, text string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < s.cfg.ReplyRetryCount; attempt++ {
		messageIDOut, err := s.feishu.ReplyText(ctx, messageID, text)
		if err == nil {
			return messageIDOut, nil
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	return "", lastErr
}

func (s *Service) replyImageWithRetry(ctx context.Context, messageID string, imagePath string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < s.cfg.ReplyRetryCount; attempt++ {
		messageIDOut, err := s.feishu.ReplyImage(ctx, messageID, imagePath)
		if err == nil {
			return messageIDOut, nil
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	return "", lastErr
}

func (s *Service) markEvent(ts time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastEventAt = &ts
}

func (s *Service) markExecution(snapshot *ExecutionSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastExec = snapshot
}

func (s *Service) shouldTriggerGroupMessage(ctx context.Context, incoming *incomingMessage) bool {
	if incoming == nil || len(incoming.Mentions) == 0 {
		log.Printf("[DEBUG] shouldTriggerGroupMessage: no mentions")
		return false
	}

	// Log mention details for debugging
	for i, m := range incoming.Mentions {
		if m == nil {
			continue
		}
		log.Printf("[DEBUG] mention[%d]: Key=%s, Name=%s, OpenId=%s, UserId=%s",
			i, value(m.Key), value(m.Name),
			value(m.Id.OpenId), value(m.Id.UserId))
	}

	// First check if any mention is a bot mention (by Key starting with @_bot_)
	// This works even if we can't fetch bot names
	if mentionsBotByKey(incoming.Mentions) {
		log.Printf("[DEBUG] shouldTriggerGroupMessage: matched by Key")
		return true
	}

	// Check if mention matches configured BotOpenID
	if s.cfg.BotOpenID != "" && mentionsBotByOpenID(incoming.Mentions, s.cfg.BotOpenID) {
		log.Printf("[DEBUG] shouldTriggerGroupMessage: matched by BotOpenID")
		return true
	}

	// Check if mention matches configured BotMentionNames
	if len(s.cfg.BotMentionNames) > 0 && mentionsBotByNames(incoming.Mentions, s.cfg.BotMentionNames) {
		log.Printf("[DEBUG] shouldTriggerGroupMessage: matched by BotMentionNames")
		return true
	}

	// Try to get bot mention names and match by name or ID
	botMentionNames, err := s.botMentionNamesForTrigger(ctx)
	if err != nil {
		log.Printf("resolve bot mention names failed: %v", err)
		// If we can't get names but Key check didn't match, return false
		return false
	}
	log.Printf("[DEBUG] botMentionNames: %v", botMentionNames)

	result := messageMentionsBot(incoming.Mentions, botMentionNames, s.cfg.AppID)
	log.Printf("[DEBUG] shouldTriggerGroupMessage: result=%v", result)
	return result
}

func (s *Service) botMentionNamesForTrigger(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	if s.botMentionNamesLoaded {
		names := append([]string(nil), s.botMentionNames...)
		s.mu.RUnlock()
		return names, nil
	}
	s.mu.RUnlock()

	if s.feishu == nil {
		return nil, errors.New("feishu client is not configured")
	}

	names, err := s.feishu.GetBotMentionNames(ctx)
	if err != nil {
		return nil, err
	}
	names = dedupeMentionNames(names)

	s.mu.Lock()
	if !s.botMentionNamesLoaded {
		s.botMentionNames = append([]string(nil), names...)
		s.botMentionNamesLoaded = true
	}
	names = append([]string(nil), s.botMentionNames...)
	s.mu.Unlock()
	return names, nil
}

func parseIncoming(event *larkim.P2MessageReceiveV1) (*incomingMessage, error) {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Sender == nil || event.Event.Sender.SenderId == nil {
		return nil, fmt.Errorf("missing event payload")
	}

	message := event.Event.Message
	chatType := value(message.ChatType)
	if chatType != "p2p" && chatType != "group" {
		return nil, ignoredEventError("ignore unsupported chat type")
	}

	messageType := value(message.MessageType)
	if messageType != "text" && messageType != "image" {
		return nil, ignoredEventError("ignore unsupported message type")
	}

	openID := value(event.Event.Sender.SenderId.OpenId)
	if openID == "" {
		return nil, fmt.Errorf("missing sender open_id")
	}

	incoming := &incomingMessage{
		Message:      message,
		MessageID:    value(message.MessageId),
		ChatID:       value(message.ChatId),
		ChatType:     chatType,
		MessageType:  messageType,
		SenderOpenID: openID,
		RawContent:   value(message.Content),
		HasMentions:  len(message.Mentions) > 0,
		Mentions:     append([]*larkim.MentionEvent(nil), message.Mentions...),
	}

	switch messageType {
	case "text":
		var body struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(incoming.RawContent), &body); err != nil {
			return nil, fmt.Errorf("parse text content: %w", err)
		}
		incoming.Text = strings.TrimSpace(body.Text)
		if incoming.Text == "" {
			return nil, ignoredEventError("ignore empty text message")
		}
	case "image":
		var body struct {
			ImageKey string `json:"image_key"`
		}
		if err := json.Unmarshal([]byte(incoming.RawContent), &body); err != nil {
			return nil, fmt.Errorf("parse image content: %w", err)
		}
		incoming.ImageKey = strings.TrimSpace(body.ImageKey)
		if incoming.ImageKey == "" {
			return nil, fmt.Errorf("missing image_key")
		}
		if incoming.ChatType != "p2p" {
			return nil, ignoredEventError("ignore non-p2p image message")
		}
	}

	return incoming, nil
}

func messageMentionsBot(mentions []*larkim.MentionEvent, botMentionNames []string, appID string) bool {
	if len(mentions) == 0 {
		return false
	}

	normalizedNames := make(map[string]struct{}, len(botMentionNames))
	for _, name := range botMentionNames {
		name = normalizeMentionIdentity(name)
		if name == "" {
			continue
		}
		normalizedNames[name] = struct{}{}
	}

	normalizedAppID := normalizeMentionIdentity(appID)
	for _, mention := range mentions {
		if mention == nil {
			continue
		}
		// Check if mention.Key is a bot mention (starts with "@_bot_") or contains appID
		if key := value(mention.Key); key != "" {
			normalizedKey := normalizeMentionIdentity(key)
			// Bot mentions in Feishu start with "@_bot_"
			if strings.HasPrefix(normalizedKey, "@_bot_") {
				return true
			}
			// Also check if Key directly contains the appID
			if strings.Contains(normalizedKey, normalizedAppID) {
				return true
			}
		}
		if mentionTargetsAppID(mention, normalizedAppID) {
			return true
		}
		if _, ok := normalizedNames[normalizeMentionIdentity(value(mention.Name))]; ok {
			return true
		}
	}
	return false
}

func mentionTargetsAppID(mention *larkim.MentionEvent, normalizedAppID string) bool {
	if mention == nil || mention.Id == nil || normalizedAppID == "" {
		return false
	}
	return normalizeMentionIdentity(value(mention.Id.OpenId)) == normalizedAppID ||
		normalizeMentionIdentity(value(mention.Id.UserId)) == normalizedAppID ||
		normalizeMentionIdentity(value(mention.Id.UnionId)) == normalizedAppID
}

func mentionsBotByKey(mentions []*larkim.MentionEvent) bool {
	for _, mention := range mentions {
		if mention == nil {
			continue
		}
		if key := value(mention.Key); key != "" {
			normalizedKey := normalizeMentionIdentity(key)
			// Bot mentions in Feishu start with "@_bot_"
			if strings.HasPrefix(normalizedKey, "@_bot_") {
				return true
			}
		}
	}
	return false
}

func mentionsBotByOpenID(mentions []*larkim.MentionEvent, botOpenID string) bool {
	if botOpenID == "" {
		return false
	}
	normalizedBotOpenID := normalizeMentionIdentity(botOpenID)
	for _, mention := range mentions {
		if mention == nil || mention.Id == nil {
			continue
		}
		if normalizeMentionIdentity(value(mention.Id.OpenId)) == normalizedBotOpenID {
			return true
		}
	}
	return false
}

func mentionsBotByNames(mentions []*larkim.MentionEvent, botNames []string) bool {
	if len(botNames) == 0 {
		return false
	}
	normalizedNames := make(map[string]struct{}, len(botNames))
	for _, name := range botNames {
		normalizedNames[normalizeMentionIdentity(name)] = struct{}{}
	}
	for _, mention := range mentions {
		if mention == nil {
			continue
		}
		if _, ok := normalizedNames[normalizeMentionIdentity(value(mention.Name))]; ok {
			return true
		}
	}
	return false
}

func normalizeMentionIdentity(input string) string {
	return strings.ToLower(strings.TrimSpace(input))
}

func dedupeMentionNames(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := normalizeMentionIdentity(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

func parseResponsePayload(output string) responsePayload {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	textLines := make([]string, 0, len(lines))
	imagePaths := make([]string, 0)
	wikiWrites := make([]wikiWriteAction, 0)

	inWikiWrite := false
	var wikiURL string
	var wikiMarkdown []string

	flushWikiWrite := func() {
		if strings.TrimSpace(wikiURL) == "" {
			wikiURL = ""
			wikiMarkdown = nil
			return
		}
		wikiWrites = append(wikiWrites, wikiWriteAction{
			URL:      strings.TrimSpace(wikiURL),
			Markdown: strings.TrimSpace(strings.Join(wikiMarkdown, "\n")),
		})
		wikiURL = ""
		wikiMarkdown = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inWikiWrite {
			if trimmed == wikiWriteEndMarker {
				flushWikiWrite()
				inWikiWrite = false
				continue
			}
			wikiMarkdown = append(wikiMarkdown, line)
			continue
		}
		if strings.HasPrefix(trimmed, wikiWriteBeginPrefix) && strings.HasSuffix(trimmed, imageMarkerSuffix) {
			wikiURL = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, wikiWriteBeginPrefix), imageMarkerSuffix))
			inWikiWrite = true
			continue
		}
		if strings.HasPrefix(trimmed, imageMarkerPrefix) && strings.HasSuffix(trimmed, imageMarkerSuffix) {
			imagePath := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, imageMarkerPrefix), imageMarkerSuffix))
			if imagePath != "" {
				imagePaths = append(imagePaths, imagePath)
			}
			continue
		}
		textLines = append(textLines, line)
	}
	if inWikiWrite {
		flushWikiWrite()
	}
	return responsePayload{
		Text:       strings.TrimSpace(strings.Join(textLines, "\n")),
		ImagePaths: imagePaths,
		WikiWrites: wikiWrites,
	}
}

func imageExtension(fileName string, data []byte) string {
	if ext := filepath.Ext(fileName); ext != "" {
		return ext
	}
	if len(data) == 0 {
		return ".img"
	}
	contentType := http.DetectContentType(data)
	exts, err := mime.ExtensionsByType(contentType)
	if err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".img"
}

func excludeMessage(entries []store.ConversationEntry, messageID string) []store.ConversationEntry {
	if messageID == "" {
		return entries
	}
	filtered := make([]store.ConversationEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.MessageID == messageID {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func reverseConversationEntries(entries []store.ConversationEntry) {
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
}

func messageEndTimeSeconds(message *larkim.EventMessage) string {
	if message == nil {
		return ""
	}
	raw := strings.TrimSpace(value(message.CreateTime))
	if raw == "" {
		return ""
	}
	millis, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return ""
	}
	seconds := millis / 1000
	if millis%1000 != 0 {
		seconds++
	}
	return strconv.FormatInt(seconds, 10)
}

type ignoredEventError string

func (e ignoredEventError) Error() string {
	return string(e)
}

func isIgnoredEvent(err error) bool {
	var ignored ignoredEventError
	return errors.As(err, &ignored)
}

func joinErrors(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "; ")
}

func joinNonEmpty(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, "; ")
}

func firstMessageID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func pairingApprovalReplyText(httpAddr string, openID string) string {
	endpoint := "http://" + strings.TrimSpace(httpAddr)
	return fmt.Sprintf("配对申请已收到。请在 server 主机执行以下命令完成授权：\ncurl -X POST %s/v1/pairing/requests/%s/approve", endpoint, openID)
}

func maskOpenID(input string) string {
	if len(input) <= 8 {
		return input
	}
	return input[:4] + "..." + input[len(input)-4:]
}

func cloneTime(ts *time.Time) *time.Time {
	if ts == nil {
		return nil
	}
	value := *ts
	return &value
}

func value(input *string) string {
	if input == nil {
		return ""
	}
	return *input
}

func randomID(prefix string) string {
	var buffer [8]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buffer[:])
}
