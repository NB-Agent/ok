// ok-slack-bot — bridges Slack to an OK Agent via the imbot framework.
//
// Uses Slack Events API over HTTP webhook + Slack Web API for posting messages.
//
// Setup:
//
//  1. Create a Slack App at https://api.slack.com/apps
//  2. Enable Event Subscriptions with Request URL: https://your-host/slack/events
//  3. Add OAuth scope: chat:write, im:history, groups:history
//  4. Install the app to your workspace
//  5. Set env vars:
//     SLACK_BOT_TOKEN=xoxb-...
//     SLACK_SIGNING_SECRET=abc...
//
// Usage:
//
//	ok-slack-bot -listen :8083
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	"strconv"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/imbot"
)

// ─── Slack Adapter ─────────────────────────────────────────────────────────

type slackAdapter struct {
	token   string
	baseURL string
	client  *http.Client
}

func newSlackAdapter(token string) *slackAdapter {
	return &slackAdapter{
		token:   token,
		baseURL: "https://slack.com/api",
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (a *slackAdapter) PlatformName() string { return "slack" }

func (a *slackAdapter) SendMessage(ctx context.Context, channelID, text string) error {
	// Slack uses channel IDs, not user IDs. The userID stored by our bot
	// is actually the channel ID (DM or channel).
	payload := map[string]any{
		"channel": channelID,
		"text":    text,
		"mrkdwn":  true,
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/chat.postMessage", &buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	var r struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if !r.OK {
		return fmt.Errorf("slack API error: %s", r.Error)
	}
	return nil
}

func (a *slackAdapter) SendTyping(_ context.Context, _ string) {
	// Slack doesn't have a typing indicator API for bots. No-op.
}

// ─── Event Verification ────────────────────────────────────────────────────

// verifySlackSignature checks the X-Slack-Signature and X-Slack-Request-Timestamp
// headers to authenticate incoming webhook requests from Slack.
func verifySlackSignature(signingSecret string, body []byte, signature string, timestamp string) bool {
	if signature == "" || timestamp == "" {
		return false
	}
	// Check timestamp is recent (within 5 minutes)
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if time.Since(time.Unix(ts, 0)) > 5*time.Minute {
		return false
	}

	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(body)
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// ─── HTTP Server ───────────────────────────────────────────────────────────

type bot struct {
	slack         *slackAdapter
	core          *imbot.BotCore
	signingSecret string
}

func newBot(token, signingSecret, okBin, okModel, workDir string) *bot {
	adapter := newSlackAdapter(token)
	return &bot{
		slack:         adapter,
		core:          imbot.NewBotCore(adapter, okBin, okModel, workDir),
		signingSecret: signingSecret,
	}
}

func (b *bot) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /slack/events", b.handleEvent)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok","platform":"slack"}`)
	})
	return mux
}

// SlackEvent is the outer envelope of all Slack Events API payloads.
type SlackEvent struct {
	Token     string           `json:"token"`
	Challenge string           `json:"challenge,omitempty"`
	Type      string           `json:"type"`
	Event     *json.RawMessage `json:"event,omitempty"`
	EventTime int64            `json:"event_ts,omitempty"`
	TeamID    string           `json:"team_id,omitempty"`
	APIAppID  string           `json:"api_app_id,omitempty"`
}

// InnerEvent is the parsed inner event from Slack.
type InnerEvent struct {
	Type      string `json:"type"`
	User      string `json:"user"`
	Text      string `json:"text"`
	Channel   string `json:"channel"`
	EventTime string `json:"event_ts"`
}

func (b *bot) handleEvent(w http.ResponseWriter, r *http.Request) {
	// Verify signature
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusInternalServerError)
		return
	}

	sig := r.Header.Get("X-Slack-Signature")
	ts := r.Header.Get("X-Slack-Request-Timestamp")
	if !verifySlackSignature(b.signingSecret, body, sig, ts) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var envelope SlackEvent
	if err := json.Unmarshal(body, &envelope); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	// URL verification challenge
	if envelope.Type == "url_verification" {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, envelope.Challenge)
		return
	}

	// Acknowledge immediately (Slack expects 200 within 3 seconds)
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")

	// Handle message events
	if envelope.Type != "event_callback" || envelope.Event == nil {
		return
	}

	var ev InnerEvent
	if err := json.Unmarshal(*envelope.Event, &ev); err != nil {
		log.Printf("slack: parse event: %v", err)
		return
	}

	// Only handle message events from users (not bot messages)
	if ev.Type != "message" || ev.User == "" || ev.Text == "" {
		return
	}

	// Skip bot's own messages
	if ev.User == "USLACKBOT" {
		return
	}

	// Use channel ID as the user ID for DM routing
	ctx := r.Context()
	userID := ev.Channel
	userName := ev.User
	text := strings.TrimSpace(ev.Text)

	// Handle commands
	switch {
	case text == "/start" || text == "hello":
		if err := b.slack.SendMessage(ctx, userID, "👋 Hello! I am OK Agent Bot. Send me a message and I'll help you out."); err != nil {
			log.Printf("send start: %v", err)
		}
		return
	case text == "/new":
		b.core.Sessions.Remove(userID)
		if err := b.slack.SendMessage(ctx, userID, "✅ New conversation started. What can I help you with?"); err != nil {
			log.Printf("send new: %v", err)
		}
		return
	}

	s := b.core.GetOrCreateSession(ctx, userID, userName)
	if s == nil {
		if err := b.slack.SendMessage(ctx, userID, "❌ Failed to create session. Please try again."); err != nil {
			log.Printf("send session-failed: %v", err)
		}
		return
	}
	b.core.SendPrompt(ctx, s, text)
}

// ─── Main ──────────────────────────────────────────────────────────────────

func main() {
	token := flag.String("token", os.Getenv("SLACK_BOT_TOKEN"), "Slack Bot User OAuth Token (xoxb-)")
	signingSecret := flag.String("signing-secret", os.Getenv("SLACK_SIGNING_SECRET"), "Slack Signing Secret")
	okBin := flag.String("ok-bin", findOK(), "path to ok binary")
	okModel := flag.String("ok-model", "", "model name")
	workDir := flag.String("work-dir", ".", "working directory")
	listen := flag.String("listen", ":8083", "HTTP listen address")
	flag.Parse()

	if *token == "" || *signingSecret == "" {
		log.Fatal("SLACK_BOT_TOKEN and SLACK_SIGNING_SECRET required")
	}
	if _, err := os.Stat(*okBin); err != nil {
		log.Fatalf("ok binary not found at %s", *okBin)
	}

	b := newBot(*token, *signingSecret, *okBin, *okModel, *workDir)
	log.Printf("🤖 Slack bot starting on %s", *listen)
	log.Printf("   Set Event Subscription URL: https://YOUR-DOMAIN/slack/events")
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
