// ok-discord-bot — Discord bot via imbot framework.
// Uses Discord Interactions Webhook (HTTP) + REST API.
//
// Setup:
//
//	set DISCORD_PUBLIC_KEY=abc...
//	set DISCORD_BOT_TOKEN=MTE4...
//	ok-discord-bot
//	(set Interactions Endpoint to https://host:8082/interactions)
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
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
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/imbot"
	oklog "github.com/NB-Agent/ok/internal/log"
)

type discordClient struct {
	token  string
	httpC  *http.Client
	apiURL string
}

func newDiscordClient(token string) *discordClient {
	return &discordClient{
		token:  token,
		apiURL: "https://discord.com/api/v10",
		httpC:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (d *discordClient) sendMsg(ctx context.Context, channelID, text string) error {
	payload := map[string]string{"content": text}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", d.apiURL+"/channels/"+channelID+"/messages", &buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+d.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.httpC.Do(req)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return err
	}
	defer oklog.Close("discord sendMsg response", resp.Body)
	return nil
}

func (d *discordClient) createDM(ctx context.Context, userID string) (string, error) {
	payload := map[string]string{"recipient_id": userID}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return "", fmt.Errorf("encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", d.apiURL+"/users/@me/channels", &buf)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+d.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.httpC.Do(req)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return "", err
	}
	defer oklog.Close("discord createDM response", resp.Body)
	var r struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return r.ID, nil
}

func verifyDiscordReq(publicKey string, r *http.Request, body []byte) bool {
	sig := r.Header.Get("X-Signature-Ed25519")
	ts := r.Header.Get("X-Signature-Timestamp")
	if sig == "" || ts == "" {
		return false
	}
	pubKey, err := hex.DecodeString(publicKey)
	if err != nil {
		return false
	}
	sigBytes, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	msg := append([]byte(ts), body...)
	return ed25519.Verify(pubKey, msg, sigBytes)
}

type discordAdapter struct {
	cli     *discordClient
	dmCache map[string]string
	mu      sync.Mutex
}

func newDiscordAdapter(cli *discordClient) *discordAdapter {
	return &discordAdapter{
		cli:     cli,
		dmCache: make(map[string]string),
	}
}

func (a *discordAdapter) PlatformName() string { return "discord" }

func (a *discordAdapter) SendMessage(ctx context.Context, userID, text string) error {
	parts := strings.SplitN(userID, ":", 2)
	if len(parts) < 2 {
		return fmt.Errorf("bad userID: %s", userID)
	}
	var channelID string
	switch parts[0] {
	case "ch":
		channelID = parts[1]
	case "user":
		a.mu.Lock()
		var ok bool
		channelID, ok = a.dmCache[parts[1]]
		a.mu.Unlock()
		if !ok {
			var err error
			channelID, err = a.cli.createDM(ctx, parts[1])
			if err != nil {
				return err
			}
			a.mu.Lock()
			a.dmCache[parts[1]] = channelID
			a.mu.Unlock()
		}
	default:
		channelID = parts[1]
	}
	return a.cli.sendMsg(ctx, channelID, text)
}

func (a *discordAdapter) SendTyping(_ context.Context, _ string) {}

type interaction struct {
	Type      int    `json:"type"`
	Token     string `json:"token"`
	ChannelID string `json:"channel_id"`
	Data      *struct {
		Name    string `json:"name"`
		Options []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"options,omitempty"`
	} `json:"data,omitempty"`
	Member *struct {
		User struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"user"`
	} `json:"member,omitempty"`
}

type server struct {
	core      *imbot.BotCore
	adapter   *discordAdapter
	cli       *discordClient
	publicKey string
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read error", 400)
		return
	}
	if !verifyDiscordReq(s.publicKey, r, body) {
		http.Error(w, "bad signature", 401)
		return
	}
	var it interaction
	if err := json.Unmarshal(body, &it); err != nil {
		http.Error(w, "parse error", 400)
		return
	}
	if it.Type == 1 {
		if err := json.NewEncoder(w).Encode(map[string]int{"type": 1}); err != nil {
			log.Printf("write type-1 response: %v", err)
		}
		return
	}
	if it.Type == 2 {
		s.handleCmd(w, &it)
		return
	}
	if err := json.NewEncoder(w).Encode(map[string]int{"type": 1}); err != nil {
		log.Printf("write type-1 response: %v", err)
	}
}

func (s *server) handleCmd(w http.ResponseWriter, it *interaction) {
	userID := ""
	userName := ""
	if it.Member != nil {
		userID = it.Member.User.ID
		userName = it.Member.User.Username
	}
	if userID == "" || it.Data == nil {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"type": 4,
			"data": map[string]string{"content": "could not identify user"},
		}); err != nil {
			log.Printf("write error response: %v", err)
		}
		return
	}

	ctx := context.Background()

	switch it.Data.Name {
	case "chat":
		text := ""
		for _, opt := range it.Data.Options {
			if opt.Name == "message" {
				text = opt.Value
				break
			}
		}
		if text == "" {
			if err := json.NewEncoder(w).Encode(map[string]any{
				"type": 4,
				"data": map[string]string{"content": "usage: /chat message: <text>"},
			}); err != nil {
				log.Printf("write usage response: %v", err)
			}
			return
		}
		w.WriteHeader(200)
		if err := json.NewEncoder(w).Encode(map[string]int{"type": 5}); err != nil {
			log.Printf("write ack response: %v", err)
		}
		userKey := "ch:" + it.ChannelID
		session := s.core.GetOrCreateSession(ctx, userKey, userName)
		if session == nil {
			if err := s.cli.sendMsg(ctx, it.ChannelID, "session creation failed"); err != nil {
				log.Printf("send session-failed msg: %v", err)
			}
			return
		}
		s.core.SendPrompt(ctx, session, text)
		if err := s.cli.sendMsg(ctx, it.ChannelID, "processing your request..."); err != nil {
			log.Printf("send ack: %v", err)
		}

	case "new":
		userKey := "ch:" + it.ChannelID
		s.core.Sessions.Remove(userKey)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"type": 4,
			"data": map[string]string{"content": "new conversation started"},
		}); err != nil {
			log.Printf("write new response: %v", err)
		}

	case "help":
		if err := json.NewEncoder(w).Encode(map[string]any{
			"type": 4,
			"data": map[string]string{"content": "OK Agent Bot:\n/chat message: <text> - chat with AI\n/new - new conversation\n/help - this help"},
		}); err != nil {
			log.Printf("write help response: %v", err)
		}
	default:
		if err := json.NewEncoder(w).Encode(map[string]any{
			"type": 4,
			"data": map[string]string{"content": fmt.Sprintf("unknown command: %s", it.Data.Name)},
		}); err != nil {
			log.Printf("write unknown-cmd response: %v", err)
		}
	}
}

func registerCommands(token string) error {
	commands := []map[string]any{
		{
			"name": "chat", "description": "Chat with the AI Agent",
			"options": []map[string]any{
				{"type": 3, "name": "message", "description": "your message", "required": true},
			},
		},
		{"name": "new", "description": "Start a new conversation"},
		{"name": "help", "description": "Show help"},
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(commands); err != nil {
		return fmt.Errorf("encode commands: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "PUT", "https://discord.com/api/v10/applications/@me/commands", &buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return err
	}
	defer oklog.Close("discord register response", resp.Body)
	log.Printf("discord: commands registered")
	return nil
}

func main() {
	publicKey := flag.String("public-key", os.Getenv("DISCORD_PUBLIC_KEY"), "discord public key")
	botToken := flag.String("bot-token", os.Getenv("DISCORD_BOT_TOKEN"), "discord bot token")
	okBin := flag.String("ok-bin", findOK(), "ok binary path")
	okModel := flag.String("ok-model", "", "model name")
	workDir := flag.String("work-dir", ".", "working directory")
	listen := flag.String("listen", ":8082", "http listen")
	flag.Parse()

	if *publicKey == "" || *botToken == "" {
		log.Fatal("DISCORD_PUBLIC_KEY and DISCORD_BOT_TOKEN required")
	}
	if _, err := os.Stat(*okBin); err != nil {
		log.Fatalf("ok binary not found: %s", *okBin)
	}

	cli := newDiscordClient(*botToken)
	adapter := newDiscordAdapter(cli)
	core := imbot.NewBotCore(adapter, *okBin, *okModel, *workDir)

	if err := registerCommands(*botToken); err != nil {
		log.Printf("register commands: %v", err)
	}

	srv := &server{core: core, adapter: adapter, cli: cli, publicKey: *publicKey}
	http.Handle("/interactions", srv)
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("ok")); err != nil {
			log.Printf("health write: %v", err)
		}
	})

	log.Printf("discord bot on %s (ok: %s)", *listen, *okBin)
	log.Printf("set Interactions Endpoint to https://YOUR-DOMAIN/interactions")
	if err := http.ListenAndServe(*listen, nil); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
