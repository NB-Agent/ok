// ok-dingtalk-bot — bridges DingTalk (钉钉) to an OK Agent via the imbot framework.
//
// Uses DingTalk Robot webhook + callback URL pattern.
//
// Setup:
//
//  1. Go to https://open.dingtalk.com → Robot
//  2. Create a robot, get its webhook access_token
//  3. Set up callback URL (outgoing webhook)
//  4. Set env vars:
//     DINGTALK_WEBHOOK_TOKEN=your-robot-access-token
//     DINGTALK_WEBHOOK_URL=https://oapi.dingtalk.com/robot/send?access_token=...
//
// Usage:
//
//	ok-dingtalk-bot -listen :8084
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
	"path/filepath"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/imbot"
)

// ─── DingTalk Adapter ──────────────────────────────────────────────────────

type dingtalkAdapter struct {
	webhookURL string
	client     *http.Client
}

func newDingtalkAdapter(webhookURL string) *dingtalkAdapter {
	return &dingtalkAdapter{
		webhookURL: webhookURL,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *dingtalkAdapter) PlatformName() string { return "dingtalk" }

func (a *dingtalkAdapter) SendMessage(ctx context.Context, userID, text string) error {
	// DingTalk robot sends messages to a specific user by staffId
	payload := map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": text},
		"at":      map[string]any{"atUserIds": []string{userID}},
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.webhookURL, &buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("read dingtalk response: %w", readErr)
	}
	var r struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if r.ErrCode != 0 {
		return fmt.Errorf("dingtalk error %d: %s", r.ErrCode, r.ErrMsg)
	}
	return nil
}

func (a *dingtalkAdapter) SendTyping(_ context.Context, _ string) {}

// ─── HTTP Server ───────────────────────────────────────────────────────────

// DingTalkCallback is the incoming message format from DingTalk outgoing webhook.
type DingTalkCallback struct {
	SenderID       string `json:"senderId"`
	ConversationID string `json:"conversationId"`
	ChatbotUserID  string `json:"chatbotUserId"`
	MsgType        string `json:"msgType"`
	Text           struct {
		Content string `json:"content"`
	} `json:"text"`
	SenderStaffID string `json:"senderStaffId"`
	SenderNick    string `json:"senderNick"`
}

type bot struct {
	dt   *dingtalkAdapter
	core *imbot.BotCore
}

func newBot(webhookURL, okBin, okModel, workDir string) *bot {
	return &bot{
		dt:   newDingtalkAdapter(webhookURL),
		core: imbot.NewBotCore(newDingtalkAdapter(webhookURL), okBin, okModel, workDir),
	}
}

func (b *bot) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /dingtalk", b.handleCallback)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok","platform":"dingtalk"}`)
	})
	return mux
}

func (b *bot) handleCallback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusInternalServerError)
		return
	}

	var cb DingTalkCallback
	if err := json.Unmarshal(body, &cb); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	// Only handle text messages
	if cb.MsgType != "text" || strings.TrimSpace(cb.Text.Content) == "" {
		if err := json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"}); err != nil {
			log.Printf("write response: %v", err)
		}
		return
	}

	ctx := r.Context()
	userID := cb.SenderStaffID
	userName := cb.SenderNick
	if userName == "" {
		userName = cb.SenderID
	}
	text := strings.TrimSpace(cb.Text.Content)

	switch {
	case text == "/start" || text == "hello":
		if err := b.dt.SendMessage(ctx, userID, "👋你好！我是 OK Agent Bot。发送消息即可开始对话。"); err != nil {
			log.Printf("send start: %v", err)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"}); err != nil {
			log.Printf("write response: %v", err)
		}
		return
	case text == "/new":
		b.core.Sessions.Remove(userID)
		if err := b.dt.SendMessage(ctx, userID, "✅ 已新建对话。"); err != nil {
			log.Printf("send new: %v", err)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"}); err != nil {
			log.Printf("write response: %v", err)
		}
		return
	}

	s := b.core.GetOrCreateSession(ctx, userID, userName)
	if s == nil {
		if err := b.dt.SendMessage(ctx, userID, "❌ 创建会话失败，请稍后重试。"); err != nil {
			log.Printf("send session-failed: %v", err)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"}); err != nil {
			log.Printf("write response: %v", err)
		}
		return
	}
	b.core.SendPrompt(ctx, s, text)
	if err := json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"}); err != nil {
		log.Printf("write response: %v", err)
	}
}

func main() {
	webhookURL := flag.String("webhook", os.Getenv("DINGTALK_WEBHOOK_URL"), "DingTalk robot webhook URL")
	okBin := flag.String("ok-bin", findOK(), "path to ok binary")
	okModel := flag.String("ok-model", "", "model name")
	workDir := flag.String("work-dir", ".", "working directory")
	listen := flag.String("listen", ":8084", "HTTP listen address")
	flag.Parse()

	if *webhookURL == "" {
		log.Fatal("DINGTALK_WEBHOOK_URL required")
	}
	if _, err := os.Stat(*okBin); err != nil {
		log.Fatalf("ok binary not found at %s", *okBin)
	}

	b := newBot(*webhookURL, *okBin, *okModel, *workDir)
	log.Printf("🤖 DingTalk bot starting on %s", *listen)
	log.Printf("   Webhook URL: http://YOUR-DOMAIN/dingtalk")
	if err := http.ListenAndServe(*listen, b.handler()); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

func findOK() string {
	for _, c := range []string{"ok", "ok.exe", "../ok.exe", "../../ok.exe"} {
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
