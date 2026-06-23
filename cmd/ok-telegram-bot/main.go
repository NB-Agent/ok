// ok-telegram-bot — bridges Telegram to an OK Agent via the imbot framework.
//
// Usage:
//
//	set TELEGRAM_BOT_TOKEN=123456:ABC-DEF...
//	ok-telegram-bot
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/imbot"
	oklog "github.com/NB-Agent/ok/internal/log"
)

type telegramAdapter struct {
	baseURL string
	client  *http.Client
}

func newTelegramAdapter(token string) *telegramAdapter {
	return &telegramAdapter{
		baseURL: "https://api.telegram.org/bot" + token,
		client: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:    10,
				IdleConnTimeout: 30 * time.Second,
			},
		},
	}
}

func (a *telegramAdapter) PlatformName() string { return "telegram" }

func (a *telegramAdapter) SendMessage(ctx context.Context, userID, text string) error {
	chatID, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid user ID %q: %w", userID, err)
	}
	payload := map[string]any{
		"chat_id":    chatID,
		"text":       escapeMarkdown(text),
		"parse_mode": "MarkdownV2",
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/sendMessage", &buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer oklog.Close("telegram sendMessage response", resp.Body)
	return nil
}

func (a *telegramAdapter) SendTyping(ctx context.Context, userID string) {
	vals := url.Values{}
	vals.Set("chat_id", userID)
	vals.Set("action", "typing")
	req, err := http.NewRequestWithContext(ctx, "GET", a.baseURL+"/sendChatAction?"+vals.Encode(), nil)
	if err != nil {
		return
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return
	}
	defer oklog.Close("telegram sendChatAction response", resp.Body)
}

func escapeMarkdown(s string) string {
	for _, ch := range []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"} {
		s = strings.ReplaceAll(s, ch, "\\"+ch)
	}
	return s
}

type bot struct {
	tg     *telegramAdapter
	core   *imbot.BotCore
	httpC  *http.Client
	offset int64
}

func newBot(token, okBin, okModel, workDir string) *bot {
	adapter := newTelegramAdapter(token)
	return &bot{
		tg:    adapter,
		core:  imbot.NewBotCore(adapter, okBin, okModel, workDir),
		httpC: &http.Client{Timeout: 60 * time.Second},
	}
}

func (b *bot) run(ctx context.Context) error {
	log.Printf("🤖 telegram bot started (OK: %s)", b.core.OKBin)
	for {
		select {
		case <-ctx.Done():
			b.core.Sessions.ShutdownAll()
			return ctx.Err()
		default:
		}
		updates, err := b.poll(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				continue
			}
			log.Printf("poll error: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, upd := range updates {
			b.handleUpdate(ctx, upd)
		}
	}
}

type tgUpdate struct {
	UpdateID int64  `json:"update_id"`
	Message  *tgMsg `json:"message,omitempty"`
}
type tgMsg struct {
	Chat tgChat `json:"chat"`
	Text string `json:"text,omitempty"`
	From tgFrom `json:"from,omitempty"`
}
type tgChat struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}
type tgFrom struct {
	Username string `json:"username,omitempty"`
}

func (b *bot) poll(ctx context.Context) ([]tgUpdate, error) {
	vals := url.Values{}
	vals.Set("offset", strconv.FormatInt(b.offset, 10))
	vals.Set("timeout", "30")
	vals.Set("allowed_updates", `["message"]`)

	req, err := http.NewRequestWithContext(ctx, "GET", b.tg.baseURL+"/getUpdates?"+vals.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.httpC.Do(req)
	if err != nil {
		return nil, err
	}
	defer oklog.Close("telegram poll response", resp.Body)

	var apiResp struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode poll response: %w", err)
	}
	if !apiResp.OK {
		return nil, fmt.Errorf("tg API error")
	}
	var updates []tgUpdate
	if err := json.Unmarshal(apiResp.Result, &updates); err != nil {
		return nil, fmt.Errorf("decode updates: %w", err)
	}
	return updates, nil
}

func (b *bot) handleUpdate(ctx context.Context, upd tgUpdate) {
	b.offset = upd.UpdateID + 1
	if upd.Message == nil || upd.Message.Text == "" {
		return
	}

	userID := strconv.FormatInt(upd.Message.Chat.ID, 10)
	text := strings.TrimSpace(upd.Message.Text)
	userName := upd.Message.From.Username

	switch {
	case text == "/start":
		if err := b.tg.SendMessage(ctx, userID, "👋 你好！我是 OK Agent Bot。\n\n发送消息即可与 AI 对话。\n命令：\n  /new — 新建对话\n  /help — 帮助信息"); err != nil {
			log.Printf("send start: %v", err)
		}
		return
	case text == "/help":
		if err := b.tg.SendMessage(ctx, userID, "🤖 OK Agent Bot\n\n命令：\n  /new — 新建对话\n  /help — 显示帮助\n\n直接发送消息即可与 AI 对话。"); err != nil {
			log.Printf("send help: %v", err)
		}
		return
	case text == "/new":
		b.core.Sessions.Remove(userID)
		if err := b.tg.SendMessage(ctx, userID, "✅ 已新建对话。"); err != nil {
			log.Printf("send new: %v", err)
		}
		return
	}

	s := b.core.GetOrCreateSession(ctx, userID, userName)
	if s == nil {
		if err := b.tg.SendMessage(ctx, userID, "❌ 创建会话失败，请稍后重试。"); err != nil {
			log.Printf("send session failed: %v", err)
		}
		return
	}
	go b.tg.SendTyping(context.Background(), userID)
	b.core.SendPrompt(ctx, s, text)
}

func main() {
	token := flag.String("token", os.Getenv("TELEGRAM_BOT_TOKEN"), "Telegram Bot API token")
	okBin := flag.String("ok-bin", findOK(), "path to ok binary")
	okModel := flag.String("ok-model", "", "model name")
	workDir := flag.String("work-dir", ".", "working directory")
	flag.Parse()

	if *token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN required")
	}
	if _, err := os.Stat(*okBin); err != nil {
		log.Fatalf("ok binary not found at %s", *okBin)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	b := newBot(*token, *okBin, *okModel, *workDir)
	if err := b.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("bot error: %v", err)
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
