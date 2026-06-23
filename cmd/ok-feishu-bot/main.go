// ok-feishu-bot — Feishu (Lark) bot via imbot framework.
//
// Setup:
//
//	set FEISHU_APP_ID=cli_...
//	set FEISHU_APP_SECRET=...
//	ok-feishu-bot
//
// Webhook: http://host:8081/feishu
// WebSocket: ws://host:8081/ws
// Health:    http://host:8081/health
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/imbot"
	oklog "github.com/NB-Agent/ok/internal/log"
)

// ─────────────────────────────────────────────────────────────────────────────
// Feishu API client (token management + messaging)
// ─────────────────────────────────────────────────────────────────────────────

type feishuClient struct {
	appID, appSecret string
	httpC            *http.Client
	mu               sync.Mutex
	token            string
	tokenExp         time.Time
}

func newFeishuClient(appID, appSecret string) *feishuClient {
	return &feishuClient{
		appID:     appID,
		appSecret: appSecret,
		httpC: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:    5,
				IdleConnTimeout: 90 * time.Second,
			},
		},
	}
}

// getToken returns a valid tenant_access_token, fetching a new one if expired.
// Uses double-checked locking to prevent thundering herd on concurrent calls.
func (c *feishuClient) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && time.Now().Before(c.tokenExp) {
		t := c.token
		c.mu.Unlock()
		return t, nil
	}
	c.mu.Unlock()

	payload := map[string]string{"app_id": c.appID, "app_secret": c.appSecret}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return "", fmt.Errorf("encode token payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", &buf)
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.httpC.Do(req)
	if err != nil {
		return "", fmt.Errorf("token HTTP: %w", err)
	}
	defer oklog.Close("feishu token response", resp.Body)

	var r struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if r.Code != 0 {
		return "", fmt.Errorf("token err %d: %s", r.Code, r.Msg)
	}

	c.mu.Lock()
	c.token = r.TenantAccessToken
	// Refresh 5 minutes before expiry to avoid edge cases with clock skew
	c.tokenExp = time.Now().Add(time.Duration(r.Expire) * time.Second).Add(-5 * time.Minute)
	t := c.token
	c.mu.Unlock()

	log.Printf("feishu: token refreshed (expires in %ds)", r.Expire)
	return t, nil
}

func (c *feishuClient) sendMessage(ctx context.Context, receiveID, text string) error {
	log.Printf("feishu: sendMessage to %s (len=%d)", receiveID, len(text))

	token, err := c.getToken(ctx)
	if err != nil {
		return fmt.Errorf("getToken: %w", err)
	}

	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("marshal content: %w", err)
	}
	payload := map[string]any{
		"receive_id": receiveID,
		"msg_type":   "text",
		"content":    string(content),
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}

	u := "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=open_id"
	req, err := http.NewRequestWithContext(ctx, "POST", u, &buf)
	if err != nil {
		return fmt.Errorf("create message request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpC.Do(req)
	if err != nil {
		return fmt.Errorf("send HTTP: %w", err)
	}
	defer oklog.Close("feishu send response", resp.Body)

	// Read all for logging
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read send response: %w", err)
	}
	snippet := string(bodyBytes)
	if len(snippet) > 500 {
		snippet = snippet[:500]
	}
	log.Printf("feishu: send response (HTTP %d): %s", resp.StatusCode, snippet)

	var r struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			MessageID string `json:"message_id"`
		} `json:"data,omitempty"`
	}
	if err := json.Unmarshal(bodyBytes, &r); err != nil {
		return fmt.Errorf("decode send response: %w", err)
	}
	if r.Code != 0 {
		return fmt.Errorf("send err %d: %s", r.Code, r.Msg)
	}
	if r.Data == nil || r.Data.MessageID == "" {
		log.Printf("feishu: send returned code=0 but no message_id — may not deliver")
	} else {
		log.Printf("feishu: message sent OK, id=%s", r.Data.MessageID)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Feishu adapter (implements imbot.Adapter)
// ─────────────────────────────────────────────────────────────────────────────

type feishuAdapter struct{ cli *feishuClient }

func (a *feishuAdapter) PlatformName() string { return "feishu" }

func (a *feishuAdapter) SendMessage(ctx context.Context, userID, text string) error {
	return a.cli.sendMessage(ctx, userID, text)
}

// SendTyping is a no-op — Feishu does not expose a typing indicator API via
// tenant token. The platform shows "..." automatically while the bot processes.
func (a *feishuAdapter) SendTyping(_ context.Context, _ string) {}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP server (webhook handler)
// ─────────────────────────────────────────────────────────────────────────────

type server struct {
	core    *imbot.BotCore
	adapter *feishuAdapter
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("feishu: read body error: %v", err)
		http.Error(w, "read body", http.StatusInternalServerError)
		return
	}
	log.Printf("feishu: webhook received (len=%d): %s", len(bodyBytes), string(bodyBytes[:min(len(bodyBytes), 300)]))

	var cb struct {
		Challenge string `json:"challenge,omitempty"`
		Encrypt   string `json:"encrypt,omitempty"`
		Event     *struct {
			Sender struct {
				SenderID struct {
					UserID string `json:"user_id"`
					OpenID string `json:"open_id"`
				} `json:"sender_id"`
			} `json:"sender"`
			Message struct {
				MsgType     string `json:"msg_type"`     // v1.0 callback
				MessageType string `json:"message_type"` // v2.0 schema
				Content     string `json:"content"`
			} `json:"message"`
		} `json:"event,omitempty"`
	}
	if err := json.Unmarshal(bodyBytes, &cb); err != nil {
		log.Printf("feishu: unmarshal callback: %v", err)
		if err := json.NewEncoder(w).Encode(map[string]int{"code": 0}); err != nil {
			log.Printf("feishu: write response: %v", err)
		}
		return
	}

	// ── Challenge verification (required by Feishu for webhook setup) ──────
	if cb.Challenge != "" {
		log.Printf("feishu: challenge verification")
		if err := json.NewEncoder(w).Encode(map[string]string{"challenge": cb.Challenge}); err != nil {
			log.Printf("feishu: write challenge: %v", err)
		}
		return
	}

	// ── Encrypted payload — tell user to disable encryption ────────────────
	if cb.Encrypt != "" {
		log.Printf("feishu: encrypted payload — disable encryption in event config")
		if err := json.NewEncoder(w).Encode(map[string]int{"code": 0}); err != nil {
			log.Printf("feishu: write response: %v", err)
		}
		return
	}

	if cb.Event == nil {
		log.Printf("feishu: no event object in payload")
		if err := json.NewEncoder(w).Encode(map[string]int{"code": 0}); err != nil {
			log.Printf("feishu: write response: %v", err)
		}
		return
	}

	// Support both v1.0 (msg_type) and v2.0 (message_type) callback formats.
	msgType := cb.Event.Message.MsgType
	if msgType == "" {
		msgType = cb.Event.Message.MessageType
	}
	log.Printf("feishu: event msg_type=%q", msgType)
	if msgType != "text" {
		log.Printf("feishu: ignoring non-text message type: %q", msgType)
		if err := json.NewEncoder(w).Encode(map[string]int{"code": 0}); err != nil {
			log.Printf("feishu: write response: %v", err)
		}
		return
	}

	var content struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(cb.Event.Message.Content), &content); err != nil {
		log.Printf("feishu: parse content error: %v (content=%q)", err, cb.Event.Message.Content)
		if err := json.NewEncoder(w).Encode(map[string]int{"code": 0}); err != nil {
			log.Printf("feishu: write response: %v", err)
		}
		return
	}
	text := strings.TrimSpace(content.Text)
	log.Printf("feishu: received text: %q", text)

	userID := cb.Event.Sender.SenderID.OpenID
	if userID == "" {
		userID = cb.Event.Sender.SenderID.UserID
	}
	log.Printf("feishu: sender openID=%q", userID)

	if userID == "" {
		log.Printf("feishu: empty sender ID — skipping")
		if err := json.NewEncoder(w).Encode(map[string]int{"code": 0}); err != nil {
			log.Printf("feishu: write response: %v", err)
		}
		return
	}

	// Use background context for all API calls — handler returns immediately.
	bg := context.Background()

	switch {
	case text == "/start":
		log.Printf("feishu: handling /start")
		msg := "👋 你好！我是 OK Agent Bot。\n\n" +
			"发送消息即可与 AI 对话。\n\n" +
			"命令：\n" +
			"  /new — 新建对话\n" +
			"  /help — 帮助信息"
		if err := s.adapter.SendMessage(bg, userID, msg); err != nil {
			log.Printf("feishu: send start error: %v", err)
		}
		if err := json.NewEncoder(w).Encode(map[string]int{"code": 0}); err != nil {
			log.Printf("feishu: write response: %v", err)
		}
		return

	case text == "/help":
		log.Printf("feishu: handling /help")
		msg := "🤖 OK Agent Bot — 飞书版\n\n" +
			"命令：\n" +
			"  /start — 开始对话\n" +
			"  /new   — 新建对话\n" +
			"  /help  — 显示帮助\n\n" +
			"直接发送消息即可与 AI 对话。\n\n" +
			"WebSocket: ws://host:8081/ws"
		if err := s.adapter.SendMessage(bg, userID, msg); err != nil {
			log.Printf("feishu: send help error: %v", err)
		}
		if err := json.NewEncoder(w).Encode(map[string]int{"code": 0}); err != nil {
			log.Printf("feishu: write response: %v", err)
		}
		return

	case text == "/new":
		log.Printf("feishu: handling /new")
		s.core.Sessions.Remove(userID)
		if err := s.adapter.SendMessage(bg, userID, "✅ 已新建对话。"); err != nil {
			log.Printf("feishu: send new error: %v", err)
		}
		if err := json.NewEncoder(w).Encode(map[string]int{"code": 0}); err != nil {
			log.Printf("feishu: write response: %v", err)
		}
		return
	}

	log.Printf("feishu: creating/getting ACP session for user=%s", userID)
	session := s.core.GetOrCreateSession(bg, userID, userID)
	if session == nil {
		log.Printf("feishu: session creation failed for user=%s", userID)
		if err := s.adapter.SendMessage(bg, userID, "❌ 创建会话失败，请稍后重试。"); err != nil {
			log.Printf("feishu: send session failed error: %v", err)
		}
		if err := json.NewEncoder(w).Encode(map[string]int{"code": 0}); err != nil {
			log.Printf("feishu: write response: %v", err)
		}
		return
	}
	log.Printf("feishu: ACP session ready, dispatching prompt (len=%d)", len(text))
	go s.core.SendPrompt(bg, session, text)
	if err := json.NewEncoder(w).Encode(map[string]int{"code": 0}); err != nil {
		log.Printf("feishu: write response: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Entry point
// ─────────────────────────────────────────────────────────────────────────────

func main() {
	// Log to file + stderr (critical on Windows where console disappears on crash)
	logFile, err := os.OpenFile("ok-feishu-bot.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	} else {
		log.Printf("feishu: cannot open log file: %v (stderr only)", err)
	}
	if logFile != nil {
		defer logFile.Close()
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("feishu: PANIC: %v", r)
			if logFile != nil {
				if syncErr := logFile.Sync(); syncErr != nil {
					log.Printf("feishu: sync log on panic: %v", syncErr)
				}
			}
			os.Exit(1)
		}
	}()

	log.Printf("feishu: starting...")
	if _, err := config.Load(); err != nil {
		log.Printf("feishu: config.Load: %v", err)
	} else {
		log.Printf("feishu: config loaded")
	}

	appID := flag.String("app-id", os.Getenv("FEISHU_APP_ID"), "feishu app id")
	appSecret := flag.String("app-secret", os.Getenv("FEISHU_APP_SECRET"), "feishu app secret")
	okBin := flag.String("ok-bin", findOK(), "ok binary path")
	okModel := flag.String("ok-model", "", "model name (provider/model)")
	workDir := flag.String("work-dir", ".", "working directory")
	listen := flag.String("listen", ":8081", "http listen address")
	flag.Parse()

	if *appID == "" || *appSecret == "" {
		log.Fatal("FEISHU_APP_ID and FEISHU_APP_SECRET are required")
	}
	if _, err := os.Stat(*okBin); err != nil {
		log.Printf("feishu: ok binary not found at %s, will rely on PATH", *okBin)
		*okBin = "ok"
	}

	// ── Build the bot ─────────────────────────────────────────────────────
	adapter := &feishuAdapter{cli: newFeishuClient(*appID, *appSecret)}
	core := imbot.NewBotCore(adapter, *okBin, *okModel, *workDir)
	core.EnableWebSocket() // enable WS streaming for real-time clients
	srv := &server{core: core, adapter: adapter}

	// ── HTTP routes ────────────────────────────────────────────────────────
	mux := http.NewServeMux()
	mux.Handle("/feishu", srv)
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		imbot.ServeWS(core, w, r)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok", "platform": "feishu"}); err != nil {
			log.Printf("feishu: write health response: %v", err)
		}
	})

	httpServer := &http.Server{
		Addr:         *listen,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// ── Graceful shutdown on SIGINT/SIGTERM ───────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("feishu: shutdown goroutine panic: %v", r)
			}
		}()
		<-ctx.Done()
		log.Printf("feishu: shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("feishu: shutdown error: %v", err)
		}
		core.Sessions.ShutdownAll()
		if logFile != nil {
			if syncErr := logFile.Sync(); syncErr != nil {
				log.Printf("feishu: sync log on shutdown: %v", syncErr)
			}
		}
	}()

	log.Printf("🤖 feishu bot listening on %s (ok: %s, model: %s)", *listen, *okBin, *okModel)
	log.Printf("feishu: webhook → http://<host>%s/feishu", *listen)
	log.Printf("feishu: websocket → ws://<host>%s/ws", *listen)
	log.Printf("feishu: health    → http://<host>%s/health", *listen)

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}

	log.Printf("feishu: stopped")
	if logFile != nil {
		if syncErr := logFile.Sync(); syncErr != nil {
			log.Printf("feishu: sync log on stop: %v", syncErr)
		}
	}
}

// findOK searches for the ok binary in common locations.
func findOK() string {
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	candidates := []string{
		filepath.Join(exeDir, "..", "ok.exe"),
		filepath.Join(exeDir, "..", "..", "ok.exe"),
		filepath.Join(exeDir, "ok.exe"),
		filepath.Join(exeDir, "bin", "ok.exe"),
		filepath.Join(exeDir, "build", "bin", "ok.exe"),
		"ok.exe", "ok",
		"../ok.exe", "../../ok.exe",
		"bin/ok.exe", "build/bin/ok.exe",
	}
	for _, c := range candidates {
		if p, e := exec.LookPath(c); e == nil {
			if a, err := filepath.Abs(p); err == nil {
				return a
			}
			return p
		}
		if _, e := os.Stat(c); e == nil {
			if a, err := filepath.Abs(c); err == nil {
				return a
			}
			return c
		}
	}
	return "ok"
}
