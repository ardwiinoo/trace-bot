// Package httpapi is the web channel adapter: a small JSON API for the React UI.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tracebot/internal/agent"
)

type Server struct {
	agent  *agent.Agent
	origin string
	token  string
}

func New(ag *agent.Agent, allowedOrigin, token string) *Server {
	return &Server{agent: ag, origin: allowedOrigin, token: token}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("POST /api/chat", s.chat)
	mux.HandleFunc("POST /api/reset", s.reset)
	return s.cors(s.auth(mux))
}

// Serve blocks until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	slog.Info("http api listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") == s.origin {
			w.Header().Set("Access-Control-Allow-Origin", s.origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// auth requires a bearer token on /api/* when WEB_API_TOKEN is set.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodOptions {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type chatRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		req.SessionID = "default"
	}
	res, err := s.agent.Ask(r.Context(), "web-"+req.SessionID, req.Message)
	if err != nil {
		slog.Error("ask failed", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	if req.SessionID == "" {
		req.SessionID = "default"
	}
	s.agent.Reset("web-" + req.SessionID)
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
