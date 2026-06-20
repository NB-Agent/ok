// Package imbot WebSocket support — adds WebSocket long-connection alongside
// HTTP webhook for real-time bidirectional communication with an OK Agent.
//
// Architecture:
//
//	WebSocket client ←→ ws.ServeWS ←→ BotCore ←→ ACP subprocess
//
// Each connected WebSocket client can submit prompts and receive streaming
// responses in real-time, independently of the IM platform webhook path.
package imbot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

// ─── WebSocket handler ────────────────────────────────────────────────────

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(_ *http.Request) bool { return true },
}

// wsCommand is every client→server message over the bot WebSocket.
type wsCommand struct {
	Type  string `json:"type"`            // "submit", "cancel", "new_session"
	Input string `json:"input,omitempty"` // text input for "submit"
}

// ServeWS handles a WebSocket connection, bridging it to the BotCore.
//
// Protocol:
//
//	Client → Server:
//	  {"type":"submit","input":"hello"}
//	  {"type":"cancel"}
//	  {"type":"new_session"}
//
//	Server → Client:
//	  {"kind":"text","text":"Hello!"}
//	  {"kind":"tool","name":"bash","args":"ls -la"}
//	  {"kind":"done"}
//	  {"kind":"error","err":"..."}
func ServeWS(bc *BotCore, w http.ResponseWriter, r *http.Request) {
	if bc.wsBroadcaster == nil {
		http.Error(w, "WebSocket not enabled", http.StatusServiceUnavailable)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "imbot/ws: upgrade: %v\n", err)
		return
	}
	defer conn.Close()

	// Subscribe to broadcaster
	eventCh, unsub := bc.wsBroadcaster.Subscribe()
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
				fmt.Fprintf(os.Stderr, "imbot/ws: write pump panic: %v\n", r)
			}
		}()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case data, ok := <-eventCh:
				if !ok {
					conn.WriteMessage(websocket.CloseMessage,
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

	// ── Read pump: WebSocket commands → BotCore ────────────────────────
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "imbot/ws: read pump panic: %v\n", r)
			}
		}()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var cmd wsCommand
			if err := json.Unmarshal(message, &cmd); err != nil {
				wsWriteError(conn, "bad JSON: "+err.Error())
				continue
			}

			switch cmd.Type {
			case "submit":
				if cmd.Input == "" {
					wsWriteError(conn, "submit: 'input' is required")
					continue
				}
				bg := context.Background()
				session := bc.GetOrCreateSession(bg, "_ws_"+conn.RemoteAddr().String(), "ws")
				if session == nil {
					wsWriteError(conn, "session creation failed")
					continue
				}
				bc.SendPrompt(bg, session, cmd.Input)

			case "cancel":
				wsWriteError(conn, "cancel not supported")

			case "new_session":
				userID := "_ws_" + conn.RemoteAddr().String()
				bc.Sessions.Remove(userID)
				wsWriteEvent(conn, &wsEvent{Kind: "text", Text: "New conversation started."})

			default:
				wsWriteError(conn, fmt.Sprintf("unknown command: %q", cmd.Type))
			}
		}
	}()

	// Block until either pump exits.
	select {
	case <-readDone:
	case <-writeDone:
	}
}

func wsWriteEvent(conn *websocket.Conn, e *wsEvent) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = conn.WriteMessage(websocket.TextMessage, data)
}

func wsWriteError(conn *websocket.Conn, msg string) {
	wsWriteEvent(conn, &wsEvent{Kind: "error", Err: msg})
}
