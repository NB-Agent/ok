// Package bridge defines the agent-to-agent protocol that allows OK
// instances to discover each other and share tasks. Multiple OK agents
// on a local network (or same machine) can form an ad-hoc team
// without any central server — pure peer-to-peer.
//
// Discovery: mDNS (Bonjour/avahi) on port 9463. Each agent announces
// its name, capabilities, and current load. Discovery is zero-config.
//
// Communication: HTTP/JSON over the same port. Agents RPC each other
// with task requests, returning results as streaming NDJSON.
//
// Security: local-network only (bind 127.0.0.1 or LAN interface).
// Shared secret (from ~/.ok/agent-secret) prevents unauthorized peers.
package bridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/log"
)

// httpClient is a package-level HTTP client with timeout for bridge RPC.
var httpClient = &http.Client{Timeout: 60 * time.Second}

// AgentInfo describes an agent on the network.
type AgentInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Version  string    `json:"version"`
	Model    string    `json:"model"`
	Load     int       `json:"load"` // 0-100
	Tags     []string  `json:"tags"` // desktop, server, coding, personal
	Addr     string    `json:"addr"` // ip:port
	LastSeen time.Time `json:"-"`
}

// TaskRequest is sent from one agent to another.
type TaskRequest struct {
	From    string `json:"from"`
	Task    string `json:"task"`
	Timeout int    `json:"timeout_sec"`
}

// TaskResponse is the streaming result.
type TaskResponse struct {
	Seq        int    `json:"seq"`
	Text       string `json:"text,omitempty"`
	Done       bool   `json:"done"`
	Error      string `json:"error,omitempty"`
	TokenUsage int    `json:"tokens,omitempty"`
}

// Bridge manages agent-to-agent connections.
type Bridge struct {
	mu       sync.RWMutex
	peers    map[string]*AgentInfo // id → info
	secret   string
	port     int
	onTask   func(ctx context.Context, task string) (<-chan string, error)
	listener net.Listener
	httpSrv  *http.Server
	done     chan struct{}
	stopOnce sync.Once
}

// NewBridge creates an agent bridge. If port is 0, the system picks a random
// available port (read via ListenerAddr after Start). Default port is 9463.
func NewBridge(secret string, port int, onTask func(ctx context.Context, task string) (<-chan string, error)) *Bridge {
	if port < 0 {
		port = 9463
	}
	return &Bridge{
		peers:  make(map[string]*AgentInfo),
		secret: secret,
		port:   port,
		onTask: onTask,
		done:   make(chan struct{}),
	}
}

// Start begins listening and announcing.
func (b *Bridge) Start() error {
	// Start HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/ping", b.handlePing)
	mux.HandleFunc("/api/v1/task", b.handleTask)

	// When port is 0, listen on :0 (random port) and capture the assigned port.
	addr := fmt.Sprintf(":%d", b.port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("bridge listen: %w", err)
	}
	b.listener = l
	if b.port == 0 {
		if tcpAddr, ok := l.Addr().(*net.TCPAddr); ok {
			b.port = tcpAddr.Port
		}
	}

	b.httpSrv = &http.Server{Handler: mux, ReadTimeout: 60 * time.Second}
	go b.httpSrv.Serve(l)

	return nil
}

// Stop shuts down the bridge.
func (b *Bridge) Stop() {
	b.stopOnce.Do(func() {
		close(b.done)
		if b.httpSrv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := b.httpSrv.Shutdown(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "bridge: graceful shutdown: %v — forcing close\n", err)
				b.httpSrv.Close()
			}
		}
	})
}

// Peers returns currently known peers.
func (b *Bridge) Peers() []AgentInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []AgentInfo
	for _, p := range b.peers {
		out = append(out, *p)
	}
	return out
}

// SendTask sends a task to a peer and returns the streaming result.
func (b *Bridge) SendTask(ctx context.Context, peerAddr, task string) (<-chan TaskResponse, error) {
	url := fmt.Sprintf("http://%s/api/v1/task", peerAddr)
	body, err := json.Marshal(TaskRequest{From: b.secretID(), Task: task, Timeout: 300})
	if err != nil {
		return nil, fmt.Errorf("marshal task request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OK-Secret", b.secret)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bridge send: %w", err)
	}

	ch := make(chan TaskResponse, 32)
	go func() {
		defer log.Close("bridge response", resp.Body)
		defer close(ch)
		defer func() {
			if r := recover(); r != nil {
				ch <- TaskResponse{Done: true, Error: fmt.Sprintf("bridge: panic: %v", r)}
			}
		}()
		dec := json.NewDecoder(resp.Body)
		for {
			var tr TaskResponse
			if err := dec.Decode(&tr); err != nil {
				if err != io.EOF {
					ch <- TaskResponse{Done: true, Error: err.Error()}
				}
				return
			}
			ch <- tr
			if tr.Done {
				return
			}
		}
	}()
	return ch, nil
}

func (b *Bridge) handlePing(w http.ResponseWriter, r *http.Request) {
	if !b.checkAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var info AgentInfo
	if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
		// Malformed request — treat as empty info; peer discovery won't break.
		fmt.Fprintf(os.Stderr, "bridge: ping decode: %v\n", err)
	}
	info.LastSeen = time.Now()

	b.mu.Lock()
	b.peers[info.ID] = &info
	b.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AgentInfo{ID: b.secretID()})
}

func (b *Bridge) handleTask(w http.ResponseWriter, r *http.Request) {
	if !b.checkAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	enc := json.NewEncoder(w)

	if b.onTask == nil {
		enc.Encode(TaskResponse{Done: true, Error: "no task handler"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.Timeout)*time.Second)
	defer cancel()

	ch, err := b.onTask(ctx, req.Task)
	if err != nil {
		enc.Encode(TaskResponse{Done: true, Error: err.Error()})
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	seq := 0
	for msg := range ch {
		seq++
		enc.Encode(TaskResponse{Seq: seq, Text: msg})
		if flusher != nil {
			flusher.Flush()
		}
	}
	enc.Encode(TaskResponse{Seq: seq + 1, Done: true})
	if flusher != nil {
		flusher.Flush()
	}
}

func (b *Bridge) checkAuth(r *http.Request) bool {
	given := r.Header.Get("X-OK-Secret")
	return subtle.ConstantTimeCompare([]byte(given), []byte(b.secret)) == 1
}

func (b *Bridge) secretID() string {
	sum := sha256.Sum256([]byte(b.secret))
	return "ok-" + hex.EncodeToString(sum[:8])
}

// ── Key management ──

// LoadOrCreateSecret returns the agent's shared secret (for peer auth).
// Creates one on first run. Errors are logged to stderr; a best-effort
// secret is always returned (bridge is optional, so startup is never
// blocked by a missing secret).
func LoadOrCreateSecret() string {
	path := secretPath()
	data, err := os.ReadFile(path)
	if err == nil && len(data) > 8 {
		return strings.TrimSpace(string(data))
	}
	// Generate new secret
	b := make([]byte, 32)
	rand.Read(b)
	secret := hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		fmt.Fprintf(os.Stderr, "bridge: mkdir for secret: %v\n", err)
	}
	if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "bridge: write secret: %v\n", err)
	}
	return secret
}

func secretPath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "ok", "agent-secret")
}
