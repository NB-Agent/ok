// ok-whatsapp-bot — bridges WhatsApp Cloud API to an OK Agent via imbot framework.
//
// Setup:
//
//  1. Go to https://developers.facebook.com → WhatsApp → Cloud API
//  2. Get your Phone Number ID and generate a permanent access token
//  3. Set webhook URL to https://your-host/webhook
//  4. Set verify token (any string you choose)
//
// Environment:
//
//	WHATSAPP_PHONE_ID=123456789     (WhatsApp Business Phone Number ID)
//	WHATSAPP_TOKEN=EAAToken...     (Permanent access token)
//	WHATSAPP_VERIFY_TOKEN=myverify (Webhook verification token)
//	WHATSAPP_WEBHOOK_PORT=8081     (optional, default 8081)
//
// Usage:
//
//	ok-whatsapp-bot
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
	"time"

	"github.com/NB-Agent/ok/internal/imbot"
	oklog "github.com/NB-Agent/ok/internal/log"
)

// ─── WhatsApp Cloud API Adapter ────────────────────────────────────────────

type whatsappAdapter struct {
	phoneID string
	token   string
	apiBase string
	client  *http.Client
}

func newWhatsAppAdapter(phoneID, token string) *whatsappAdapter {
	return &whatsappAdapter{
		phoneID: phoneID,
		token:   token,
		apiBase: "https://graph.facebook.com/v22.0",
		client: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:    10,
				IdleConnTimeout: 30 * time.Second,
			},
		},
	}
}

func (a *whatsappAdapter) PlatformName() string { return "whatsapp" }

func (a *whatsappAdapter) SendMessage(ctx context.Context, userID, text string) error {
	// WhatsApp Cloud API sends messages via POST to /{phone-id}/messages
	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                userID,
		"type":              "text",
		"text": map[string]string{
			"preview_url": "false",
			"body":        text,
		},
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		a.apiBase+"/"+a.phoneID+"/messages", &buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.token)

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer oklog.Close("whatsapp sendMessage response", resp.Body)

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		body, readErr := io.ReadAll(resp.Body)
		errMsg := "(cannot read body)"
		if readErr == nil {
			errMsg = string(body[:min(len(body), 500)])
		}
		return fmt.Errorf("whatsapp API error (HTTP %d): %s", resp.StatusCode, errMsg)
	}

	return nil
}

func (a *whatsappAdapter) SendTyping(ctx context.Context, userID string) {
	// WhatsApp Cloud API doesn't have a simple typing indicator like Telegram.
	// We just log it — messages arrive fast enough via the API.
}

// ─── WhatsApp Webhook Handler ──────────────────────────────────────────────

type bot struct {
	wa     *whatsappAdapter
	core   *imbot.BotCore
	verify string
}

func newBot(phoneID, token, verifyToken, okBin, okModel, workDir string) *bot {
	adapter := newWhatsAppAdapter(phoneID, token)
	return &bot{
		wa:     adapter,
		core:   imbot.NewBotCore(adapter, okBin, okModel, workDir),
		verify: verifyToken,
	}
}

func (b *bot) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /webhook", b.verifyWebhook)
	mux.HandleFunc("POST /webhook", b.handleWebhook)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok","platform":"whatsapp"}`)
	})
	return mux
}

// verifyWebhook handles the WhatsApp Cloud API webhook verification.
// WhatsApp sends a GET with hub.mode, hub.verify_token, hub.challenge.
func (b *bot) verifyWebhook(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	log.Printf("webhook verify: mode=%q token=%q challenge=%q", mode, token, challenge)

	if mode == "subscribe" && token == b.verify && challenge != "" {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, challenge)
		log.Println("✅ Webhook verified successfully")
		return
	}

	log.Printf("webhook verification failed: mode=%q token=%q (expected verify=%q)", mode, token, b.verify)
	http.Error(w, "verification failed", http.StatusForbidden)
}

// waWebhookPayload matches the WhatsApp Cloud API incoming message payload.
type waWebhookPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		Changes []struct {
			Value struct {
				Messages []waMessage `json:"messages,omitempty"`
				Contacts []struct {
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
					WaID string `json:"wa_id"`
				} `json:"contacts,omitempty"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type waMessage struct {
	From string `json:"from"`
	ID   string `json:"id"`
	Text *struct {
		Body string `json:"body"`
	} `json:"text,omitempty"`
	Type string `json:"type"`
}

// handleWebhook processes incoming WhatsApp messages.
func (b *bot) handleWebhook(w http.ResponseWriter, r *http.Request) {
	// Read body (limit to 1MB)
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var payload waWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("webhook: bad JSON: %v", err)
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}

	// WhatsApp expects a 200 OK immediately to acknowledge receipt.
	w.WriteHeader(http.StatusOK)

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, msg := range change.Value.Messages {
				// Only handle text messages
				if msg.Type != "text" || msg.Text == nil {
					continue
				}
				userID := msg.From
				text := strings.TrimSpace(msg.Text.Body)

				// Look up contact name (optional)
				userName := ""
				for _, c := range change.Value.Contacts {
					if c.WaID == userID {
						userName = c.Profile.Name
						break
					}
				}

				go b.handleMessage(context.Background(), userID, userName, text)
			}
		}
	}
}

func (b *bot) handleMessage(ctx context.Context, userID, userName, text string) {
	// Handle commands
	switch {
	case text == "/start" || text == "开始" || text == "start":
		if err := b.wa.SendMessage(ctx, userID, "👋 你好！我是 OK Agent Bot。\n\n发送消息即可与 AI 对话。\n命令：\n  /new — 新建对话\n  /help — 帮助信息"); err != nil {
			log.Printf("send start: %v", err)
		}
		return
	case text == "/help" || text == "帮助":
		if err := b.wa.SendMessage(ctx, userID, "🤖 OK Agent Bot\n\n命令：\n  /new — 新建对话\n  /help — 显示帮助\n\n直接发送消息即可与 AI 对话。"); err != nil {
			log.Printf("send help: %v", err)
		}
		return
	case text == "/new" || text == "新对话":
		b.core.Sessions.Remove(userID)
		if err := b.wa.SendMessage(ctx, userID, "✅ 已新建对话。"); err != nil {
			log.Printf("send new: %v", err)
		}
		return
	}

	// Get or create ACP session and send to agent
	s := b.core.GetOrCreateSession(ctx, userID, userName)
	if s == nil {
		if err := b.wa.SendMessage(ctx, userID, "❌ 创建会话失败，请稍后重试。"); err != nil {
			log.Printf("send session failed: %v", err)
		}
		return
	}
	b.core.SendPrompt(ctx, s, text)
}

// ─── Main ──────────────────────────────────────────────────────────────────

func main() {
	phoneID := flag.String("phone-id", os.Getenv("WHATSAPP_PHONE_ID"), "WhatsApp Business Phone Number ID")
	token := flag.String("token", os.Getenv("WHATSAPP_TOKEN"), "WhatsApp Cloud API permanent access token")
	verifyToken := flag.String("verify-token", os.Getenv("WHATSAPP_VERIFY_TOKEN"), "Webhook verification token")
	port := flag.String("port", getEnvDefault("WHATSAPP_WEBHOOK_PORT", "8081"), "Webhook HTTP port")
	okBin := flag.String("ok-bin", findOK(), "path to ok binary")
	okModel := flag.String("ok-model", "", "model name (optional)")
	workDir := flag.String("work-dir", ".", "working directory")
	flag.Parse()

	if *phoneID == "" {
		log.Fatal("WHATSAPP_PHONE_ID required (or -phone-id flag)")
	}
	if *token == "" {
		log.Fatal("WHATSAPP_TOKEN required (or -token flag)")
	}
	if *verifyToken == "" {
		log.Fatal("WHATSAPP_VERIFY_TOKEN required (or -verify-token flag)")
	}
	if _, err := os.Stat(*okBin); err != nil {
		log.Fatalf("ok binary not found at %s", *okBin)
	}

	b := newBot(*phoneID, *token, *verifyToken, *okBin, *okModel, *workDir)
	addr := ":" + *port

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	srv := &http.Server{
		Addr:         addr,
		Handler:      b.handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in shutdown: %v", r)
			}
		}()
		<-ctx.Done()
		log.Println("shutting down...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx) //nolint:errcheck
	}()

	log.Printf("🤖 WhatsApp bot starting on %s", addr)
	log.Printf("   Webhook URL: http://your-host%s/webhook", addr)
	log.Printf("   OK binary: %s", *okBin)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}

	b.core.Sessions.ShutdownAll()
	log.Println("✅ WhatsApp bot stopped")
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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
