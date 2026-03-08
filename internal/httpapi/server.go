package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/pezy/feishu-codex-bridge/internal/bridge"
	"github.com/pezy/feishu-codex-bridge/internal/store"
)

type Service interface {
	Status(context.Context) (bridge.Status, error)
	RecentConversations(context.Context, int) ([]store.ConversationEntry, error)
	SendBoundMessage(context.Context, string) (string, error)
}

type Server struct {
	httpServer *http.Server
}

func New(addr string, readTimeout time.Duration, writeTimeout time.Duration, service Service) *Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		status, err := service.Status(r.Context())
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, http.StatusOK, status)
	})

	mux.HandleFunc("/v1/conversations/recent", func(w http.ResponseWriter, r *http.Request) {
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil {
				limit = parsed
			}
		}
		entries, err := service.RecentConversations(r.Context(), limit)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"items": entries,
			"count": len(entries),
		})
	})

	mux.HandleFunc("/v1/messages/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			respondJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}

		var payload struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		if payload.Text == "" {
			respondJSON(w, http.StatusBadRequest, map[string]any{"error": "text is required"})
			return
		}

		messageID, err := service.SendBoundMessage(r.Context(), payload.Text)
		if err != nil {
			respondError(w, http.StatusBadGateway, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"message_id": messageID,
		})
	})

	return &Server{
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      mux,
			ReadTimeout:  readTimeout,
			WriteTimeout: writeTimeout,
		},
	}
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Close(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func respondError(w http.ResponseWriter, status int, err error) {
	respondJSON(w, status, map[string]any{
		"error": err.Error(),
	})
}
