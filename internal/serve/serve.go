// Package serve exposes a control.Controller over HTTP: the typed event stream
// as Server-Sent Events, and the commands as small JSON POST endpoints. It is a
// second frontend alongside the chat TUI — proof that the controller is
// transport-agnostic, and the basis for a browser/desktop client. One server
// drives one session; multiple browser tabs share it.
//
// SECURITY: By default the server binds to localhost so only local processes
// can reach it. For remote access (mobile app, team sharing), configure
// --api-key or --sso to enable authentication. Never expose an unprotected
// server on a network interface.
package serve

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/NB-Agent/ok/internal/control"
	"github.com/NB-Agent/ok/internal/enterprise"
	"github.com/NB-Agent/ok/internal/evolution"
	"github.com/NB-Agent/ok/internal/log"
)

// Pre-allocated SSE byte slices — zero allocation per event.
var (
	sseDataPrefix = []byte("data: ")
	sseMsgEnd     = []byte("\n\n")
	sseHeartbeat  = []byte(": heartbeat\n\n")
)

//go:embed index.html
var indexHTML []byte

//go:embed manifest.json
var manifestJSON []byte

//go:embed sw.js
var swJS []byte

// Server wires a controller to its HTTP surface. The Broadcaster must be the
// same sink the controller was constructed with, so events reach SSE clients.
type Server struct {
	ctrl       *control.Controller
	bc         *Broadcaster
	adminSrv   *enterprise.AdminServer
	ssoHandler func(http.Handler) http.Handler
	auth       *apiKeyAuthenticator
}

// ServerOption configures Server with enterprise features.
type ServerOption func(*Server)

// WithAdminAPI enables the admin REST API + Prometheus metrics.
func WithAdminAPI(ctrl enterprise.Controller, exporter *enterprise.AuditExporter, authorizer *enterprise.Authorizer, apiKey string) ServerOption {
	return func(s *Server) {
		s.adminSrv = enterprise.NewAdminServer(ctrl, exporter, authorizer, apiKey)
	}
}

// WithSSO enables OIDC-based authentication.
func WithSSO(issuerURL, clientID string) ServerOption {
	return func(s *Server) {
		s.ssoHandler = enterprise.OIDCMiddleware(issuerURL, clientID)
	}
}

// WithAPIKey enables API Key authentication on the server.
func WithAPIKey(key string) ServerOption {
	return func(s *Server) {
		s.auth = newAPIKeyAuthenticator(key)
	}
}

// New builds a Server. bc must be the controller's event sink.
func New(ctrl *control.Controller, bc *Broadcaster, opts ...ServerOption) *Server {
	s := &Server{ctrl: ctrl, bc: bc}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.index)
	mux.HandleFunc("GET /manifest.json", s.serveManifest)
	mux.HandleFunc("GET /sw.js", s.serveSW)
	mux.HandleFunc("GET /events", s.events)
	mux.HandleFunc("GET /history", s.history)
	mux.HandleFunc("GET /context", s.context)
	mux.HandleFunc("POST /submit", s.submit)
	mux.HandleFunc("POST /cancel", s.cancel)
	mux.HandleFunc("POST /approve", s.approve)
	mux.HandleFunc("POST /plan", s.plan)
	mux.HandleFunc("POST /compact", s.compact)
	mux.HandleFunc("POST /new", s.newSession)

	// HCP WebSocket (Human Communication Protocol)
	mux.HandleFunc("GET /ws", s.handleWS)

	// ACP protocol info
	mux.HandleFunc("GET /acp", s.acpInfo)

	// Health check
	mux.HandleFunc("GET /healthz", s.healthz)

	// Admin API
	if s.adminSrv != nil {
		s.adminSrv.RegisterRoutes(mux)
	}

	// ECP — Evolution Control Protocol (auto-enabled when controller has engine)
	if eng := s.ctrl.ECPEngine(); eng != nil {
		evolution.ServeECPWithSecret(mux, eng, s.ctrl.ECPSharedSecret())
	}

	var h http.Handler = mux
	if s.ssoHandler != nil {
		h = s.ssoHandler(mux)
	}
	if s.auth != nil {
		h = s.auth.middleware(h)
	}
	return h
}

// RunOptions configures the HTTP server.
type RunOptions struct {
	Addr    string // e.g. "127.0.0.1:3030" or "0.0.0.0:3030"
	TLSCert string // empty = plain HTTP
	TLSKey  string // empty = plain HTTP
}

// Run serves on addr until SIGINT (backward-compatible).
func (s *Server) Run(addr string) error {
	return s.RunWith(RunOptions{Addr: addr})
}

// RunWith serves with full options including TLS.
func (s *Server) RunWith(opts RunOptions) error {
	s.ctrl.EnableInteractiveApproval()
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	defer lifecycleCancel()
	s.ctrl.SetBaseContext(lifecycleCtx)

	addr := opts.Addr

	if s.auth != nil && s.auth.enabled {
		fmt.Fprintf(os.Stderr, "🔐 serve: listening on %s — API key required\n", addr)
	} else {
		fmt.Fprintf(os.Stderr, "⚠️  serve: listening on %s — no authentication; only localhost is safe\n", addr)
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      s.Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				log.Error("goroutine panic", "recover", r)
				fmt.Fprintf(os.Stderr, "serve: panic in signal handler: %v\n", r)
			}
		}()
		<-sigCh
		signal.Stop(sigCh)
		lifecycleCancel()
		ctx, cancel := context.WithTimeout(lifecycleCtx, 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx) //nolint:errcheck
	}()

	var err error
	if opts.TLSCert != "" && opts.TLSKey != "" {
		err = srv.ListenAndServeTLS(opts.TLSCert, opts.TLSKey)
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		signal.Stop(sigCh)
		close(sigCh)
		return fmt.Errorf("serve: listen: %w", err)
	}
	<-done
	return nil
}

// ── handlers ──

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(indexHTML); err != nil {
		fmt.Fprintf(os.Stderr, "serve: write index: %v\n", err)
	}
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"ok"}`)
}

func (s *Server) acpInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{
		"protocol": "acp-v1 + hcp-ws-v1",
		"status":   "available",
		"endpoint": "GET /events (SSE) or GET /ws (WebSocket/HCP)",
	})
}

func (s *Server) serveManifest(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(manifestJSON) //nolint:errcheck
}

func (s *Server) serveSW(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.WriteHeader(http.StatusOK)
	w.Write(swJS) //nolint:errcheck
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsub := s.bc.Subscribe()
	defer unsub()
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(sseDataPrefix); err != nil {
				return
			}
			if _, err := w.Write(data); err != nil {
				return
			}
			if _, err := w.Write(sseMsgEnd); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := w.Write(sseHeartbeat); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Input == "" {
		http.Error(w, "missing input", http.StatusBadRequest)
		return
	}
	s.ctrl.Submit(body.Input)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) cancel(w http.ResponseWriter, _ *http.Request) {
	s.ctrl.Cancel()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Allow   bool   `json:"allow"`
		Session bool   `json:"session"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.ID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	s.ctrl.Approve(body.ID, body.Allow, body.Session)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) plan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	s.ctrl.SetPlanMode(body.On)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) compact(w http.ResponseWriter, r *http.Request) {
	if s.ctrl.Running() {
		http.Error(w, "a turn is still running", http.StatusConflict)
		return
	}
	if err := s.ctrl.Compact(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) newSession(w http.ResponseWriter, _ *http.Request) {
	if err := s.ctrl.NewSession(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) history(w http.ResponseWriter, _ *http.Request) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	out := make([]msg, 0, 16)
	for _, m := range s.ctrl.History() {
		out = append(out, msg{Role: string(m.Role), Content: m.Content})
	}
	writeJSON(w, out)
}

func (s *Server) context(w http.ResponseWriter, _ *http.Request) {
	used, window := s.ctrl.ContextSnapshot()
	writeJSON(w, map[string]int{"used": used, "window": window})
}

func writeJSON(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: writeJSON marshal: %v\n", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "serve: write response: %v\n", err)
	}
}
