package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pezy/feishu-codex-bridge/internal/bridge"
	"github.com/pezy/feishu-codex-bridge/internal/store"
)

type Service interface {
	Status(context.Context) (bridge.Status, error)
	RecentConversations(context.Context, int) ([]store.ConversationEntry, error)
	SendBoundMessage(context.Context, string, []string) ([]string, error)
	ListPendingPairingRequests(context.Context) ([]store.PairingRequest, error)
	ApprovePairingRequest(context.Context, string) error
	RejectPairingRequest(context.Context, string) error
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
			Text       string   `json:"text"`
			ImagePaths []string `json:"image_paths"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		payload.Text = strings.TrimSpace(payload.Text)
		if payload.Text == "" && len(payload.ImagePaths) == 0 {
			respondJSON(w, http.StatusBadRequest, map[string]any{"error": "text or image_paths is required"})
			return
		}
		for _, imagePath := range payload.ImagePaths {
			if !filepath.IsAbs(imagePath) {
				respondJSON(w, http.StatusBadRequest, map[string]any{"error": "image_paths must be absolute"})
				return
			}
		}

		messageIDs, err := service.SendBoundMessage(r.Context(), payload.Text, payload.ImagePaths)
		if err != nil {
			respondError(w, http.StatusBadGateway, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"message_id":  firstMessageID(messageIDs),
			"message_ids": messageIDs,
		})
	})

	mux.HandleFunc("/v1/pairing/requests", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			respondJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}

		items, err := service.ListPendingPairingRequests(r.Context())
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"items": items,
			"count": len(items),
		})
	})

	mux.HandleFunc("/v1/pairing/requests/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			respondJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/v1/pairing/requests/")
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) != 2 {
			respondJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}

		openID := strings.TrimSpace(parts[0])
		action := strings.TrimSpace(parts[1])
		if openID == "" {
			respondJSON(w, http.StatusBadRequest, map[string]any{"error": "open_id is required"})
			return
		}

		var err error
		switch action {
		case "approve":
			err = service.ApprovePairingRequest(r.Context(), openID)
		case "reject":
			err = service.RejectPairingRequest(r.Context(), openID)
		default:
			respondJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				respondJSON(w, http.StatusNotFound, map[string]any{"error": "pairing_request_not_found"})
				return
			}
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"ok": true})
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

func firstMessageID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
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
