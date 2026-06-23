// Package acp implements the Agent Communication Protocol — a WebSocket-based
// protocol that lets any client interact with an OK agent.
//
// Handles WebSocket upgrade per RFC 6455: SHA1 key challenge → 101 response →
// bidirectional JSON frames. Falls back to SSE for read-only streaming.
package acp

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/control"
	"github.com/NB-Agent/ok/internal/log"
)

const wsMagicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Message types
const (
	MsgSubmit  = "submit"
	MsgCancel  = "cancel"
	MsgApprove = "approve"
	MsgEvent   = "event"
)

type ClientMsg struct {
	Type     string `json:"type"`
	Input    string `json:"input,omitempty"`
	ID       string `json:"id,omitempty"`
	Allow    bool   `json:"allow,omitempty"`
	Remember bool   `json:"remember,omitempty"`
}

type ServerMsg struct {
	Type    string `json:"type"`
	Kind    string `json:"kind,omitempty"`
	Content string `json:"content,omitempty"`
	Name    string `json:"name,omitempty"`
	Args    any    `json:"args,omitempty"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Server struct {
	ctrl  *control.Controller
	mu    sync.Mutex
	conns map[string]*wsConn
}

type wsConn struct{ send chan []byte }

func NewServer(ctrl *control.Controller) *Server {
	return &Server{ctrl: ctrl, conns: make(map[string]*wsConn)}
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
			s.handleWS(w, r)
			return
		}
		s.HandleSSE(w, r)
	})
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return
	}
	// SHA1 here is mandated by RFC 6455 §4.2.2 for WebSocket accept-key derivation.
	// This is NOT a cryptographic use — the protocol requires SHA1 for the GUID handshake.
	h := sha1.New()
	h.Write([]byte(key + wsMagicGUID))
	accept := base64.StdEncoding.EncodeToString(h.Sum(nil))

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijack support", http.StatusInternalServerError)
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n") //nolint:errcheck
	bufrw.Flush()                                                                                                                                      //nolint:errcheck

	id := fmt.Sprintf("ws-%d", time.Now().UnixNano())
	wsc := &wsConn{send: make(chan []byte, 16)}
	s.mu.Lock()
	s.conns[id] = wsc
	s.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("goroutine panic", "recover", r)
				fmt.Fprintf(os.Stderr, "acp: panic in readWS: %v\n", r)
				close(wsc.send) // unblock writeWS
			}
		}()
		s.readWS(conn, id)
		close(wsc.send) // unblock writeWS when the read side drops
	}()
	s.writeWS(conn, wsc)

	s.mu.Lock()
	delete(s.conns, id)
	s.mu.Unlock()
}

func (s *Server) readWS(conn net.Conn, _ string) {
	defer log.Close("conn", conn)
	buf := make([]byte, 4096)
	var msgbuf []byte
	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second)) //nolint:errcheck
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		msgbuf = append(msgbuf, buf[:n]...)
		for len(msgbuf) >= 2 {
			opcode := msgbuf[0] & 0x0F
			masked := msgbuf[1]&0x80 != 0
			if !masked {
				return
			}
			plen := uint64(msgbuf[1] & 0x7F)
			hlen := 2
			if plen == 126 {
				if len(msgbuf) < 4 {
					break
				}
				plen = uint64(msgbuf[2])<<8 | uint64(msgbuf[3])
				hlen = 4
			} else if plen == 127 {
				if len(msgbuf) < 10 {
					break
				}
				plen = 0
				for i := range 8 {
					plen = plen<<8 | uint64(msgbuf[2+i])
				}
				hlen = 10
			}
			flen := hlen + 4 + int(plen)
			if len(msgbuf) < flen {
				break
			}
			payload := make([]byte, plen)
			mask := msgbuf[hlen : hlen+4]
			for i := range plen {
				payload[i] = msgbuf[hlen+4+int(i)] ^ mask[i%4]
			}
			if opcode == 1 {
				var msg ClientMsg
				if json.Unmarshal(payload, &msg) == nil {
					switch msg.Type {
					case MsgSubmit:
						if msg.Input != "" {
							go func() {
								defer func() {
									if r := recover(); r != nil {
										log.Error("goroutine panic", "recover", r)
									}
								}()
								s.ctrl.Submit(msg.Input)
							}()
						}
					case MsgCancel:
						s.ctrl.Cancel()
					case MsgApprove:
						s.ctrl.Approve(msg.ID, msg.Allow, msg.Remember)
					default: // ignore unknown message type
					}
				}
			} else if opcode == 8 {
				return
			} else if opcode == 9 {
				s.sendFrame(conn, 10, payload)
			}
			msgbuf = msgbuf[flen:]
		}
	}
}

func (s *Server) writeWS(conn net.Conn, wsc *wsConn) {
	for msg := range wsc.send {
		s.sendFrame(conn, 1, msg)
	}
}

func (s *Server) sendFrame(conn net.Conn, opcode byte, payload []byte) {
	frame := make([]byte, 2)
	frame[0] = 0x80 | opcode
	if len(payload) < 126 {
		frame[1] = byte(len(payload))
	} else if len(payload) < 65536 {
		frame[1] = 126
		frame = append(frame, byte(len(payload)>>8), byte(len(payload)))
	} else {
		frame[1] = 127
		for i := 7; i >= 0; i-- {
			frame = append(frame, byte(len(payload)>>(i*8)))
		}
	}
	frame = append(frame, payload...)
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	if _, err := conn.Write(frame); err != nil {
		fmt.Fprintf(os.Stderr, "acp: write frame: %v\n", err)
	}
}

func (s *Server) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	<-r.Context().Done()
}

func (s *Server) Broadcast(msg ServerMsg) {
	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acp: Broadcast marshal error: %v\n", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		select {
		case c.send <- data:
		default:
		}
	}
}
