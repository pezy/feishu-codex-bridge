package feishu

import (
	"context"
	"fmt"
	"log"
	"strings"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type BridgeHandler interface {
	HandleIncomingMessage(context.Context, *larkim.P2MessageReceiveV1) error
	AuthorizeGroup(context.Context, string) error
	RemoveAuthorizedGroup(context.Context, string) error
	MarkWSConnected()
	MarkWSError(error)
}

type wsLogger struct {
	inner   larkcore.Logger
	service BridgeHandler
}

func NewWSClient(appID string, appSecret string, service BridgeHandler, logLevel larkcore.LogLevel) *larkws.Client {
	handler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			log.Printf("[WS] received message: chat_type=%s, msg_type=%s", value(event.Event.Message.ChatType), value(event.Event.Message.MessageType))
			return service.HandleIncomingMessage(ctx, event)
		}).
		OnP2ChatMemberBotAddedV1(func(ctx context.Context, event *larkim.P2ChatMemberBotAddedV1) error {
			if event == nil || event.Event == nil {
				return nil
			}
			return service.AuthorizeGroup(ctx, value(event.Event.ChatId))
		}).
		OnP2ChatMemberBotDeletedV1(func(ctx context.Context, event *larkim.P2ChatMemberBotDeletedV1) error {
			if event == nil || event.Event == nil {
				return nil
			}
			return service.RemoveAuthorizedGroup(ctx, value(event.Event.ChatId))
		}).
		OnP2ChatDisbandedV1(func(ctx context.Context, event *larkim.P2ChatDisbandedV1) error {
			if event == nil || event.Event == nil {
				return nil
			}
			return service.RemoveAuthorizedGroup(ctx, value(event.Event.ChatId))
		}).
		OnP1AddBotV1(func(ctx context.Context, event *larkim.P1AddBotV1) error {
			if event == nil || event.Event == nil {
				return nil
			}
			return service.AuthorizeGroup(ctx, strings.TrimSpace(event.Event.OpenChatID))
		}).
		OnP1RemoveAddBotV1(func(ctx context.Context, event *larkim.P1RemoveBotV1) error {
			if event == nil || event.Event == nil {
				return nil
			}
			return service.RemoveAuthorizedGroup(ctx, strings.TrimSpace(event.Event.OpenChatID))
		})

	logger := &wsLogger{
		inner:   larkcore.NewDefaultLogger(logLevel),
		service: service,
	}

	return larkws.NewClient(
		appID,
		appSecret,
		larkws.WithEventHandler(handler),
		larkws.WithLogLevel(logLevel),
		larkws.WithLogger(logger),
	)
}

func (l *wsLogger) Debug(ctx context.Context, args ...interface{}) {
	l.inner.Debug(ctx, args...)
}

func (l *wsLogger) Info(ctx context.Context, args ...interface{}) {
	message := join(args...)
	if strings.Contains(message, "connected to") {
		l.service.MarkWSConnected()
	}
	l.inner.Info(ctx, args...)
}

func (l *wsLogger) Warn(ctx context.Context, args ...interface{}) {
	l.inner.Warn(ctx, args...)
}

func (l *wsLogger) Error(ctx context.Context, args ...interface{}) {
	message := join(args...)
	l.service.MarkWSError(fmtError(message))
	l.inner.Error(ctx, args...)
}

func join(args ...interface{}) string {
	return strings.TrimSpace(strings.ReplaceAll(fmt.Sprint(args...), "\n", " "))
}

func fmtError(message string) error {
	if message == "" {
		return nil
	}
	return &wsError{message: message}
}

type wsError struct {
	message string
}

func (e *wsError) Error() string {
	return e.message
}
