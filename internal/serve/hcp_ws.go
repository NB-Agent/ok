package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"

	"github.com/NB-Agent/ok/internal/event"
)

// ─── HCP (Human Communication Protocol) WebSocket ─────────────────────────
//
// Endpoint: GET /ws
// Protocol: Bidirectional JSON over WebSocket (RFC 6455)
//
// Client → Server (JSON commands):
//
//	{"type":"submit",      "input":"hello"}
//	{"type":"cancel"}
//	{"type":"approve",     "id":"...", "allow":true, "session":false}
//	{"type":"answer",      "id":"...", "answers":[{"questionId":"q1","selected":["A"]}]}
//	{"type":"plan",        "on":true}
//	{"type":"compact"}
//	{"type":"new_session"}
//	{"type":"history"}                    → response: {"kind":"history","messages":[...]}
//	{"type":"context"}                    → response: {"kind":"context_response","used":N,"window":N}
//
// Server → Client (JSON events — same schema as SSE data: lines):
//
//	{"kind":"text","text":"..."}
//	{"kind":"tool_dispatch","tool":{"id":"...","name":"bash","args":"...","readOnly":false}}
//	{"kind":"tool_result","tool":{"id":"...","name":"bash","output":"...","err":"","truncated":false}}
//	{"kind":"usage","usage":{"promptTokens":100,...}}
//	{"kind":"approval","approval":{"id":"...","tool":"write_file","subject":"..."}}
//	{"kind":"ask","ask":{"id":"...","questions":[...]}}
//	{"kind":"done"}
//	{"kind":"error","err":"..."}

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(_ *http.Request) bool { return true },
}

// wsCommand is every client→server message over the HCP WebSocket.
type wsCommand struct {
	Type    string          `json:"type"`
	Input   string          `json:"input,omitempty"`
	ID      string          `json:"id,omitempty"`
	Allow   *bool           `json:"allow,omitempty"`
	Session *bool           `json:"session,omitempty"`
	On      *bool           `json:"on,omitempty"`
	Answers json.RawMessage `json:"answers,omitempty"`
}

// answerPayload matches one element of the answers array.
type answerPayload struct {
	QuestionID string   `json:"questionId"`
	Selected   []string `json:"selected"`
}

// handleWS upgrades HTTP to WebSocket and runs the HCP protocol loop.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	// Check authentication before upgrade.
	if s.auth != nil && !s.auth.canUpgradeWS(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hcp: upgrade: %v\n", err)
		return
	}
	defer conn.Close()

	eventCh, unsub := s.bc.Subscribe()
	defer unsub()

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// ── Write pump: broadcaster events → WebSocket ─────────────────────
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "hcp: write pump panic: %v\n", r)
			}
		}()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case data, ok := <-eventCh:
				if !ok {
					conn.WriteMessage(websocket.CloseMessage, //nolint:errcheck
						websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"))
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					return
				}
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			case <-r.Context().Done():
				return
			}
		}
	}()

	// ── Read pump: WebSocket commands → controller ─────────────────────
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "hcp: read pump panic: %v\n", r)
			}
		}()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var cmd wsCommand
			if err := json.Unmarshal(message, &cmd); err != nil {
				hcpWSError(conn, "bad JSON: "+err.Error())
				continue
			}

			switch cmd.Type {
			case "submit":
				if cmd.Input == "" {
					hcpWSError(conn, "submit: 'input' is required")
					continue
				}
				s.ctrl.Submit(cmd.Input)

			case "cancel":
				s.ctrl.Cancel()

			case "approve":
				if cmd.ID == "" {
					hcpWSError(conn, "approve: 'id' is required")
					continue
				}
				allow := cmd.Allow != nil && *cmd.Allow
				session := cmd.Session != nil && *cmd.Session
				s.ctrl.Approve(cmd.ID, allow, session)

			case "answer":
				if cmd.ID == "" {
					hcpWSError(conn, "answer: 'id' is required")
					continue
				}
				var payloads []answerPayload
				if err := json.Unmarshal(cmd.Answers, &payloads); err != nil {
					hcpWSError(conn, "answer: bad 'answers' array")
					continue
				}
				answers := make([]event.AskAnswer, 0, len(payloads))
				for _, p := range payloads {
					answers = append(answers, event.AskAnswer{
						QuestionID: p.QuestionID,
						Selected:   p.Selected,
					})
				}
				s.ctrl.Answer(cmd.ID, answers)

			case "plan":
				on := cmd.On != nil && *cmd.On
				s.ctrl.SetPlanMode(on)

			case "compact":
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := s.ctrl.Compact(ctx); err != nil {
					hcpWSError(conn, "compact: "+err.Error())
				}

			case "new_session":
				if err := s.ctrl.NewSession(); err != nil {
					hcpWSError(conn, "new_session: "+err.Error())
				}

			case "history":
				type historyMsg struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}
				out := make([]historyMsg, 0, 16)
				for _, m := range s.ctrl.History() {
					out = append(out, historyMsg{Role: string(m.Role), Content: m.Content})
				}
				data, err := json.Marshal(map[string]any{"kind": "history", "messages": out})
				if err != nil {
					fmt.Fprintf(os.Stderr, "hcp ws: marshal history: %v\n", err)
					continue
				}
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					fmt.Fprintf(os.Stderr, "hcp ws: write history: %v\n", err)
				}

			case "context":
				used, window := s.ctrl.ContextSnapshot()
				data, err := json.Marshal(map[string]any{"kind": "context_response", "used": used, "window": window})
				if err != nil {
					fmt.Fprintf(os.Stderr, "hcp ws: marshal context: %v\n", err)
					continue
				}
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					fmt.Fprintf(os.Stderr, "hcp ws: write context: %v\n", err)
				}

			default:
				hcpWSError(conn, fmt.Sprintf("unknown command: %q", cmd.Type))
			}
		}
	}()

	// Block until either pump exits.
	select {
	case <-readDone:
	case <-writeDone:
	}
}

func hcpWSError(conn *websocket.Conn, msg string) {
	data, err := json.Marshal(map[string]string{"kind": "error", "err": msg})
	if err != nil {
		fmt.Fprintf(os.Stderr, "hcp ws: marshal error frame: %v\n", err)
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		fmt.Fprintf(os.Stderr, "hcp ws: write error frame: %v\n", err)
	}
}
