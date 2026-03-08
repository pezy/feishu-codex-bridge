package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	feishu *feishu.Client
	runner *codex.Runner

	mu          sync.RWMutex
	wsRunning   bool
	wsConnected bool
	lastError   string
	lastEventAt *time.Time
	lastConnAt  *time.Time
	lastExec    *ExecutionSnapshot
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

func (s *Service) SendBoundMessage(ctx context.Context, text string) (string, error) {
	id, err := s.feishu.SendTextToOpenID(ctx, s.cfg.AuthorizedOpenID, text)
	if err != nil {
		return "", err
	}
	if err := s.store.AppendConversation(ctx, store.ConversationEntry{
		Source:    "assistant",
		OpenID:    s.cfg.AuthorizedOpenID,
		MessageID: id,
		Content:   text,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		log.Printf("append outbound conversation: %v", err)
	}
	return id, nil
}

func (s *Service) HandleIncomingMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	message, senderOpenID, text, err := parseIncoming(event)
	if err != nil {
		if isIgnoredEvent(err) {
			return nil
		}
		return err
	}

	now := time.Now().UTC()
	s.markEvent(now)

	if senderOpenID != s.cfg.AuthorizedOpenID {
		log.Printf("ignore unauthorized sender open_id=%s", senderOpenID)
		return nil
	}

	inserted, err := s.store.TryCreateInbound(ctx, store.MessageRecord{
		MessageID:    value(message.MessageId),
		ChatID:       value(message.ChatId),
		SenderOpenID: senderOpenID,
		TextContent:  text,
		Status:       "received",
		CreatedAt:    now,
	})
	if err != nil {
		return err
	}
	if !inserted {
		return nil
	}

	if err := s.store.AppendConversation(ctx, store.ConversationEntry{
		Source:    "user",
		OpenID:    senderOpenID,
		MessageID: value(message.MessageId),
		Content:   text,
		CreatedAt: now,
	}); err != nil {
		log.Printf("append inbound conversation: %v", err)
	}

	ackReactionID, ackErr := s.feishu.AddReaction(ctx, value(message.MessageId), s.cfg.AckReactionType)
	if ackErr != nil {
		log.Printf("send ack reaction failed: %v", ackErr)
	}

	executionID := randomID("exec")
	history, err := s.store.RecentConversations(ctx, s.cfg.RecentContextLimit)
	if err != nil {
		return err
	}
	finalPrompt := prompt.Build(s.cfg.DefaultWorkDir, history, text)

	execution := store.ExecutionRecord{
		ID:               executionID,
		RequestMessageID: value(message.MessageId),
		Prompt:           finalPrompt,
		Status:           "running",
		StartedAt:        time.Now().UTC(),
	}
	if err := s.store.CreateExecution(ctx, execution); err != nil {
		return err
	}
	if err := s.store.UpdateMessageState(ctx, value(message.MessageId), "processing", "", "", executionID, "", nil); err != nil {
		return err
	}
	s.markExecution(&ExecutionSnapshot{
		RequestMessageID: value(message.MessageId),
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

	finalMessageID, replyErr := s.replyWithRetry(ctx, value(message.MessageId), responseText)
	if replyErr != nil {
		log.Printf("send final reply failed: %v", replyErr)
	}
	if ackReactionID != "" {
		if err := s.feishu.DeleteReaction(ctx, value(message.MessageId), ackReactionID); err != nil {
			log.Printf("delete ack reaction failed: %v", err)
		}
	}
	if err := s.store.AppendConversation(ctx, store.ConversationEntry{
		Source:    "assistant",
		OpenID:    senderOpenID,
		MessageID: finalMessageID,
		Content:   responseText,
		CreatedAt: completedAt,
	}); err != nil {
		log.Printf("append final conversation: %v", err)
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
	errorText := ""
	if runErr != nil {
		messageStatus = "failed"
		errorText = runErr.Error()
	}
	if replyErr != nil {
		messageStatus = "reply_failed"
		if errorText == "" {
			errorText = replyErr.Error()
		} else {
			errorText = errorText + "; " + replyErr.Error()
		}
	}
	if err := s.store.UpdateMessageState(
		ctx,
		value(message.MessageId),
		messageStatus,
		"",
		finalMessageID,
		executionID,
		errorText,
		&completedAt,
	); err != nil {
		log.Printf("update message state: %v", err)
	}

	s.markExecution(&ExecutionSnapshot{
		RequestMessageID: value(message.MessageId),
		Status:           messageStatus,
		ErrorText:        errorText,
		StartedAt:        execution.StartedAt,
		CompletedAt:      &completedAt,
	})

	return nil
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

func parseIncoming(event *larkim.P2MessageReceiveV1) (*larkim.EventMessage, string, string, error) {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Sender == nil || event.Event.Sender.SenderId == nil {
		return nil, "", "", fmt.Errorf("missing event payload")
	}

	message := event.Event.Message
	if value(message.ChatType) != "p2p" {
		return nil, "", "", ignoredEventError("ignore non-p2p message")
	}
	if value(message.MessageType) != "text" {
		return nil, "", "", ignoredEventError("ignore non-text message")
	}

	openID := value(event.Event.Sender.SenderId.OpenId)
	if openID == "" {
		return nil, "", "", fmt.Errorf("missing sender open_id")
	}

	var body struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(value(message.Content)), &body); err != nil {
		return nil, "", "", fmt.Errorf("parse text content: %w", err)
	}
	if strings.TrimSpace(body.Text) == "" {
		return nil, "", "", fmt.Errorf("empty text message")
	}

	return message, openID, strings.TrimSpace(body.Text), nil
}

type ignoredEventError string

func (e ignoredEventError) Error() string {
	return string(e)
}

func isIgnoredEvent(err error) bool {
	var ignored ignoredEventError
	return errors.As(err, &ignored)
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
