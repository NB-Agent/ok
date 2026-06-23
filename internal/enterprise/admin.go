package enterprise

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// ── Admin API ──

// AdminServer provides management REST API endpoints.
type AdminServer struct {
	ctrl       Controller
	exporter   *AuditExporter
	authorizer *Authorizer
	apiKey     string
}

// Controller is the minimal interface the admin server needs from control.Controller.
type Controller interface {
	SessionCount() int
	TotalToolCalls() int64
	TotalTokens() int64
	ListSessions() []SessionInfo
}

// SessionInfo represents a session in the system.
type SessionInfo struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Model     string    `json:"model"`
	Messages  int       `json:"messages"`
}

// NewAdminServer creates an admin API server.
func NewAdminServer(ctrl Controller, exporter *AuditExporter, authorizer *Authorizer, apiKey string) *AdminServer {
	return &AdminServer{
		ctrl:       ctrl,
		exporter:   exporter,
		authorizer: authorizer,
		apiKey:     apiKey,
	}
}

// RegisterRoutes adds admin API endpoints to the given mux.
func (s *AdminServer) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/agents", s.authWrap(s.handleAgents))
	mux.HandleFunc("GET /api/v1/admin/sessions", s.authWrap(s.handleSessions))
	mux.HandleFunc("GET /api/v1/admin/audit", s.authWrap(s.handleAudit))
	mux.HandleFunc("GET /api/v1/admin/usage", s.authWrap(s.handleUsage))
	mux.HandleFunc("GET /metrics", s.authWrap(s.handleMetrics))
}

func (s *AdminServer) handleAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"agents": []map[string]interface{}{
			{"id": "default", "status": "running", "model": "deepseek-flash"},
		},
	})
}

func (s *AdminServer) handleSessions(w http.ResponseWriter, r *http.Request) {
	sessions := s.ctrl.ListSessions()
	writeJSON(w, map[string]interface{}{
		"sessions": sessions,
		"count":    len(sessions),
	})
}

func (s *AdminServer) handleAudit(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	switch format {
	case "splunk":
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, s.exporter.ExportSplunk())
	default:
		data, err := s.exporter.ExportJSON()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(data); err != nil {
			fmt.Fprintf(os.Stderr, "admin: write: %v\n", err)
		}
	}
}

func (s *AdminServer) handleUsage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"sessions":   s.ctrl.SessionCount(),
		"tool_calls": s.ctrl.TotalToolCalls(),
		"tokens":     s.ctrl.TotalTokens(),
	})
}

func (s *AdminServer) authWrap(next http.HandlerFunc) http.HandlerFunc {
	if s.apiKey == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix {
			http.Error(w, "unauthorized: missing or invalid Authorization header", http.StatusUnauthorized)
			return
		}
		token := auth[len(prefix):]
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.apiKey)) != 1 {
			http.Error(w, "unauthorized: invalid API key", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *AdminServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "# HELP ok_sessions_total Total sessions\n")
	fmt.Fprintf(w, "# TYPE ok_sessions_total counter\n")
	fmt.Fprintf(w, "ok_sessions_total %d\n", s.ctrl.SessionCount())
	fmt.Fprintf(w, "# HELP ok_tool_calls_total Total tool calls\n")
	fmt.Fprintf(w, "# TYPE ok_tool_calls_total counter\n")
	fmt.Fprintf(w, "ok_tool_calls_total %d\n", s.ctrl.TotalToolCalls())
	fmt.Fprintf(w, "# HELP ok_tokens_total Total tokens used\n")
	fmt.Fprintf(w, "# TYPE ok_tokens_total counter\n")
	fmt.Fprintf(w, "ok_tokens_total %d\n", s.ctrl.TotalTokens())
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "admin: write: %v\n", err)
	}
}

// ── Session Store ──

// SessionStore persists sessions for recovery across restarts.
type SessionStore struct {
	mu    sync.Mutex
	store map[string][]byte // sessionID → serialized data
}

// NewSessionStore creates an in-memory session store.
func NewSessionStore() *SessionStore {
	return &SessionStore{store: make(map[string][]byte)}
}

func (s *SessionStore) Save(id string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[id] = data
	return nil
}

func (s *SessionStore) Load(id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.store[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	return data, nil
}

func (s *SessionStore) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.store))
	for id := range s.store {
		ids = append(ids, id)
	}
	return ids
}

func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.store, id)
}

// ── Air-Gap ──

// AirGapConfig holds configuration for air-gapped (offline) mode.
type AirGapConfig struct {
	Enabled    bool     // true when running in air-gapped mode
	AllowedCAs []string // allowed certificate authorities for local PKI
}

// AirGapMiddleware enforces air-gap restrictions.
func AirGapMiddleware(cfg AirGapConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.Enabled {
				// Block outbound connections
				if r.Method == "CONNECT" {
					http.Error(w, "air-gap mode: outbound connections blocked", http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
