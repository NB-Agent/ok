// Package imbot provides a shared IM bot framework for connecting messaging
// platforms (Telegram, 企业微信, 飞书, Discord, etc.) to an OK Agent via ACP.
//
// Architecture:
//
//	User ←→ IM Platform ←→ imbot.Adapter ←→ imbot.Core ←→ (ACP/stdio) ←→ ok acp
//
// Each IM platform implements Adapter; Core handles ACP session lifecycle,
// message buffering, and permission approval — shared across all platforms.
package imbot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/winhide"
)

// ──────────────────────────────────────────────
// WebSocket Broadcaster (shared by all adapters)
// ──────────────────────────────────────────────

// wsEvent is a single event pushed to WebSocket clients.
type wsEvent struct {
	Kind string `json:"kind"`           // "text", "tool", "done", "error"
	Text string `json:"text,omitempty"` // text content for "text" kind
	Err  string `json:"err,omitempty"`  // error message for "error" kind
	Name string `json:"name,omitempty"` // tool name for "tool" kind
	Args string `json:"args,omitempty"` // tool args for "tool" kind
}

// Broadcaster fans out ACP events to all connected WebSocket subscribers.
type Broadcaster struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

// NewBroadcaster returns an empty Broadcaster ready for subscribers.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subs: make(map[chan []byte]struct{}),
	}
}

// Emit marshals the event and delivers it to every subscriber. Drops to a
// subscriber whose buffer is full rather than blocking.
func (b *Broadcaster) Emit(e *wsEvent) {
	data, err := json.Marshal(e)
	if err != nil {
		fmt.Fprintf(os.Stderr, "imbot/ws: marshal event: %v\n", err)
		return
	}
	b.mu.Lock()
	chans := make([]chan []byte, 0, len(b.subs))
	for ch := range b.subs {
		chans = append(chans, ch)
	}
	b.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- data:
		default:
		}
	}
}

// Subscribe registers a new WebSocket client and returns its channel plus an
// unsubscribe func the handler must call (defer) when the client disconnects.
func (b *Broadcaster) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		close(ch)
		b.mu.Unlock()
	}
}

// ──────────────────────────────────────────────
// ACP JSON-RPC client (shared by all adapters)
// ──────────────────────────────────────────────

// ACPFrame is a JSON-RPC 2.0 frame on the wire.
type ACPFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ACPClient manages one ACP connection (one ok acp subprocess).
type ACPClient struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	sc    *bufio.Scanner

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan<- json.RawMessage
	notifH  map[string]func(json.RawMessage)
}

// NewACPClient spawns an `ok acp` subprocess and returns a connected client.
// okBin is the path to the ok binary; extraArgs are passed after "acp".
func NewACPClient(ctx context.Context, okBin string, extraArgs []string) (*ACPClient, error) {
	args := append([]string{"acp"}, extraArgs...)
	cmd := winhide.CommandContext(ctx, okBin, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ok acp: %w", err)
	}

	ac := &ACPClient{
		cmd:     cmd,
		stdin:   stdin,
		sc:      bufio.NewScanner(stdout),
		nextID:  1,
		pending: make(map[string]chan<- json.RawMessage),
		notifH:  make(map[string]func(json.RawMessage)),
	}
	return ac, nil
}

// OnNotification registers a handler for ACP notifications (e.g. session/update).
func (ac *ACPClient) OnNotification(method string, h func(json.RawMessage)) {
	ac.mu.Lock()
	ac.notifH[method] = h
	ac.mu.Unlock()
}

// StartReader begins reading ACP responses in a background goroutine.
// Must be called before any requests.
// When ctx is cancelled, the underlying subprocess is killed so the read loop exits.
func (ac *ACPClient) StartReader(ctx context.Context) {
	go ac.readLoop()
	// Kill the subprocess when ctx is cancelled so readLoop unblocks.
	go func() {
		<-ctx.Done()
		ac.Close()
	}()
}

func (ac *ACPClient) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("imbot: panic in ACP read: %v", r)
		}
	}()

	for ac.sc.Scan() {
		line := ac.sc.Bytes()
		if len(line) == 0 {
			continue
		}

		var frame ACPFrame
		if err := json.Unmarshal(line, &frame); err != nil {
			log.Printf("imbot: bad ACP frame: %v", err)
			continue
		}

		// Response — resolve pending
		if len(frame.ID) > 0 && frame.Method == "" {
			var idStr string
			if err := json.Unmarshal(frame.ID, &idStr); err != nil {
				continue
			}
			ac.mu.Lock()
			ch, ok := ac.pending[idStr]
			if ok {
				delete(ac.pending, idStr)
			}
			ac.mu.Unlock()
			if ok {
				if frame.Error != nil {
					ch <- json.RawMessage(`{"error":"` + frame.Error.Message + `"}`)
				} else if frame.Result != nil {
					ch <- frame.Result
				} else {
					ch <- json.RawMessage(`{}`)
				}
				close(ch)
			}
			continue
		}

		// Notification
		if frame.Method != "" && len(frame.ID) == 0 && frame.Params != nil {
			ac.mu.Lock()
			h := ac.notifH[frame.Method]
			ac.mu.Unlock()
			if h != nil {
				h(frame.Params)
			}
			continue
		}
	}

	if err := ac.sc.Err(); err != nil {
		log.Printf("imbot: ACP read error: %v", err)
	}
}

// Call sends a JSON-RPC request and waits for the response.
func (ac *ACPClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ac.mu.Lock()
	id := ac.nextID
	ac.nextID++
	ch := make(chan json.RawMessage, 1)
	idStr := strconv.FormatInt(id, 10)
	ac.pending[idStr] = ch
	ac.mu.Unlock()

	frame := ACPFrame{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"` + idStr + `"`),
		Method:  method,
	}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		frame.Params = p
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(frame); err != nil {
		return nil, err
	}
	if _, err := ac.stdin.Write(buf.Bytes()); err != nil {
		return nil, fmt.Errorf("write ACP: %w", err)
	}

	select {
	case result := <-ch:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Minute):
		return nil, errors.New("ACP timeout")
	}
}

// Close kills the subprocess.
func (ac *ACPClient) Close() {
	if ac.cmd != nil && ac.cmd.Process != nil {
		if err := ac.cmd.Process.Kill(); err != nil {
			log.Printf("imbot: kill process: %v", err)
		}
	}
}

// ──────────────────────────────────────────────
// ACP session management
// ──────────────────────────────────────────────

// Session represents one ACP session bound to an IM user/chat.
type Session struct {
	ID       string
	Client   *ACPClient
	UserID   string // platform-specific user/chat identifier
	UserName string
	Buf      strings.Builder
	InTurn   bool
}

// SessionManager tracks all active sessions.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session // userID → session
	byACP    map[string]string   // sessionID → userID
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
		byACP:    make(map[string]string),
	}
}

func (sm *SessionManager) Get(userID string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.sessions[userID]
}

func (sm *SessionManager) Add(userID string, s *Session) {
	sm.mu.Lock()
	sm.sessions[userID] = s
	sm.byACP[s.ID] = userID
	sm.mu.Unlock()
}

func (sm *SessionManager) Remove(userID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if s, ok := sm.sessions[userID]; ok {
		delete(sm.byACP, s.ID)
		delete(sm.sessions, userID)
	}
}

func (sm *SessionManager) GetByACP(sessionID string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	userID, ok := sm.byACP[sessionID]
	if !ok {
		return nil
	}
	return sm.sessions[userID]
}

func (sm *SessionManager) ShutdownAll() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for _, s := range sm.sessions {
		if s.Client != nil {
			s.Client.Close()
		}
	}
}

// ──────────────────────────────────────────────
// Core bot logic
// ──────────────────────────────────────────────

// Adapter is the interface each IM platform implements.
type Adapter interface {
	// SendMessage sends a text message to a user.
	SendMessage(ctx context.Context, userID, text string) error
	// SendTyping shows a typing indicator (optional).
	SendTyping(ctx context.Context, userID string)
	// PlatformName returns a human-readable name (e.g. "telegram", "企业微信").
	PlatformName() string
}

// BotCore ties together IM adapters, ACP sessions, and message handling.
type BotCore struct {
	Adapter       Adapter
	Sessions      *SessionManager
	OKBin         string
	OKModel       string
	WorkDir       string
	wsBroadcaster *Broadcaster // non-nil when WebSocket is enabled
}

// EnableWebSocket creates a Broadcaster and stores it on BotCore for WebSocket
// support. The bot should mount ServeWS on a route (e.g. "/ws").
// Returns the Broadcaster so the bot can emit custom events if needed.
func (bc *BotCore) EnableWebSocket() *Broadcaster {
	if bc.wsBroadcaster == nil {
		bc.wsBroadcaster = NewBroadcaster()
	}
	return bc.wsBroadcaster
}

// WSEnabled returns true if WebSocket support is active.
func (bc *BotCore) WSEnabled() bool {
	return bc.wsBroadcaster != nil
}

func NewBotCore(adapter Adapter, okBin, okModel, workDir string) *BotCore {
	return &BotCore{
		Adapter:  adapter,
		Sessions: NewSessionManager(),
		OKBin:    okBin,
		OKModel:  okModel,
		WorkDir:  workDir,
	}
}

// GetOrCreateSession returns an existing session or creates a new ACP session.
func (bc *BotCore) GetOrCreateSession(ctx context.Context, userID, userName string) *Session {
	if s := bc.Sessions.Get(userID); s != nil {
		return s
	}

	extraArgs := []string{}
	if bc.OKModel != "" {
		extraArgs = append(extraArgs, "-model", bc.OKModel)
	}

	ac, err := NewACPClient(ctx, bc.OKBin, extraArgs)
	if err != nil {
		log.Printf("imbot: new ACP client: %v", err)
		return nil
	}
	ac.StartReader(ctx)

	// Initialize
	initResult, err := ac.Call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo":      map[string]string{"name": "ok-" + bc.Adapter.PlatformName(), "version": "1.0.0"},
	})
	if err != nil {
		log.Printf("imbot: ACP init: %v", err)
		ac.Close()
		return nil
	}
	log.Printf("imbot: ACP initialized: %s", string(initResult))

	// Create session
	sessionResult, err := ac.Call(ctx, "session/new", map[string]any{
		"cwd": bc.WorkDir,
	})
	if err != nil {
		log.Printf("imbot: ACP session/new: %v", err)
		ac.Close()
		return nil
	}
	var sr struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(sessionResult, &sr); err != nil {
		log.Printf("imbot: parse session/new: %v", err)
		ac.Close()
		return nil
	}

	s := &Session{
		ID:       sr.SessionID,
		Client:   ac,
		UserID:   userID,
		UserName: userName,
	}
	bc.Sessions.Add(userID, s)

	// Register notification handlers
	ac.OnNotification("session/update", func(raw json.RawMessage) {
		bc.handleUpdate(raw)
	})
	ac.OnNotification("session/request_permission", func(raw json.RawMessage) {
		bc.handlePermission(raw, ac)
	})

	log.Printf("imbot: new session user=%s session=%s (%s)", userID, sr.SessionID, bc.Adapter.PlatformName())
	return s
}

// SendPrompt sends a user message to an ACP session.
func (bc *BotCore) SendPrompt(ctx context.Context, s *Session, text string) {
	log.Printf("imbot: SendPrompt session=%s len=%d", s.ID, len(text))
	params := map[string]any{
		"sessionId": s.ID,
		"prompt":    []map[string]any{{"type": "text", "text": text}},
	}
	_, err := s.Client.Call(ctx, "session/prompt", params)
	if err != nil {
		// Flush any partial text before reporting the error, in case the
		// agent managed to produce some output before the failure.
		bc.flushBuffer(s)
		log.Printf("imbot: session/prompt error: %v", err)
		if sendErr := bc.Adapter.SendMessage(ctx, s.UserID, fmt.Sprintf("❌ 错误：%v", err)); sendErr != nil {
			log.Printf("imbot: send error message: %v", sendErr)
		}
		return
	}

	log.Printf("imbot: SendPrompt done, flushing buffer (len=%d)", s.Buf.Len())
	bc.flushBuffer(s)
	if bc.wsBroadcaster != nil {
		bc.wsBroadcaster.Emit(&wsEvent{Kind: "done"})
	}
	log.Printf("imbot: SendPrompt complete for session=%s", s.ID)
}

// handleUpdate processes ACP session/update notifications.
func (bc *BotCore) handleUpdate(raw json.RawMessage) {
	var params struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}

	var upd struct {
		SessionUpdate string          `json:"sessionUpdate"`
		Content       json.RawMessage `json:"content"`
		Title         string          `json:"title,omitempty"`
	}
	if err := json.Unmarshal(params.Update, &upd); err != nil {
		log.Printf("imbot: handleUpdate unmarshal error: %v (raw=%s)", err, string(raw[:min(len(raw), 200)]))
		return
	}

	s := bc.Sessions.GetByACP(params.SessionID)
	if s == nil {
		log.Printf("imbot: handleUpdate: session not found (sid=%s)", params.SessionID)
		return
	}

	switch upd.SessionUpdate {
	case "agent_message_chunk", "agent_thought_chunk":
		var c struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		}
		if err := json.Unmarshal(upd.Content, &c); err != nil {
			return
		}
		if c.Type == "text" && c.Text != "" {
			s.Buf.WriteString(c.Text)
			s.InTurn = true
			if bc.wsBroadcaster != nil {
				bc.wsBroadcaster.Emit(&wsEvent{Kind: "text", Text: c.Text})
			}
			if shouldFlush(s.Buf.String()) {
				bc.flushBuffer(s)
			}
		}

	case "tool_call":
		title := upd.Title
		if title == "" {
			title = "工具调用"
		}
		if sendErr := bc.Adapter.SendMessage(context.Background(), s.UserID, "🔧 "+title+"..."); sendErr != nil {
			log.Printf("imbot: send tool_call: %v", sendErr)
		}
		if bc.wsBroadcaster != nil {
			bc.wsBroadcaster.Emit(&wsEvent{Kind: "tool", Name: title})
		}

	case "tool_call_update":
		bc.flushBuffer(s)
		s.InTurn = false

	default:
		if s.InTurn {
			bc.flushBuffer(s)
			s.InTurn = false
		}
	}
}

// handlePermission auto-approves (takes the first "allow" option).
func (bc *BotCore) handlePermission(raw json.RawMessage, ac *ACPClient) {
	var params struct {
		SessionID string          `json:"sessionId"`
		ToolCall  json.RawMessage `json:"toolCall"`
		Options   []struct {
			OptionID string `json:"optionId"`
		} `json:"options"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || len(params.Options) == 0 {
		return
	}

	optID := params.Options[0].OptionID
	for _, o := range params.Options {
		if strings.Contains(o.OptionID, "allow") {
			optID = o.OptionID
			break
		}
	}

	resp := map[string]any{
		"outcome": map[string]string{"outcome": "selected", "optionId": optID},
	}
	rp, err := json.Marshal(resp)
	if err != nil {
		log.Printf("imbot: marshal perm response: %v", err)
		return
	}

	frame := ACPFrame{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"perm-` + strconv.FormatInt(time.Now().UnixNano(), 10) + `"`),
		Method:  "session/request_permission/response",
		Params:  rp,
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(frame); err != nil {
		log.Printf("imbot: encode perm frame: %v", err)
		return
	}
	if _, err := ac.stdin.Write(buf.Bytes()); err != nil {
		log.Printf("imbot: write perm frame: %v", err)
	}
}

func (bc *BotCore) flushBuffer(s *Session) {
	if s.Buf.Len() == 0 {
		return
	}
	text := strings.TrimSpace(s.Buf.String())
	s.Buf.Reset()
	if text == "" {
		return
	}
	const maxMsg = 4000
	for len(text) > 0 {
		chunk := text
		if len(chunk) > maxMsg {
			chunk = chunk[:maxMsg]
			if idx := strings.LastIndex(chunk, "\n"); idx > len(chunk)/2 {
				chunk = chunk[:idx]
			}
		}
		if sendErr := bc.Adapter.SendMessage(context.Background(), s.UserID, chunk); sendErr != nil {
			log.Printf("imbot: flush chunk: %v", sendErr)
		}
		if len(chunk) >= len(text) {
			break
		}
		text = text[len(chunk):]
	}
}

func shouldFlush(s string) bool {
	if len(s) == 0 {
		return false
	}
	if len(s) > 2000 {
		return true
	}
	c := s[len(s)-1]
	return c == '.' || c == '!' || c == '?' || c == '\n' ||
		strings.HasSuffix(s, "。") || strings.HasSuffix(s, "！") || strings.HasSuffix(s, "？")
}
