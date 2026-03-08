package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pezy/feishu-codex-bridge/internal/bridge"
	"github.com/pezy/feishu-codex-bridge/internal/store"
)

type fakeService struct{}

func (fakeService) Status(context.Context) (bridge.Status, error) {
	return bridge.Status{Service: "feishu-codex-bridge"}, nil
}

func (fakeService) RecentConversations(context.Context, int) ([]store.ConversationEntry, error) {
	return []store.ConversationEntry{
		{Source: "user", Content: "hello", CreatedAt: time.Now().UTC()},
	}, nil
}

func (fakeService) SendBoundMessage(context.Context, string) (string, error) {
	return "om_123", nil
}

func TestStatusEndpoint(t *testing.T) {
	server := New("127.0.0.1:0", time.Second, time.Second, fakeService{})
	handler := server.httpServer.Handler

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload bridge.Status
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Service != "feishu-codex-bridge" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestSendMessageEndpoint(t *testing.T) {
	server := New("127.0.0.1:0", time.Second, time.Second, fakeService{})
	handler := server.httpServer.Handler

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/send", strings.NewReader(`{"text":"ping"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"message_id":"om_123"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}
