// ok-wechat-bot — Enterprise WeChat bot via imbot framework.
//
// Setup:
//
//	set WECOM_CORP_ID=ww...
//	set WECOM_AGENT_SECRET=...
//	set WECOM_AGENT_ID=1000001
//	ok-wechat-bot
//
// Webhook: http://host:8080/callback
// API: https://developer.work.weixin.qq.com/document/path/90236
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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/imbot"
	oklog "github.com/NB-Agent/ok/internal/log"
)

type wecomClient struct {
	corpID, agentSecret, agentID string
	httpC                        *http.Client
	mu                           sync.Mutex
	token                        string
	tokenExp                     time.Time
}

func newWecomClient(corpID, agentSecret, agentID string) *wecomClient {
	return &wecomClient{
		corpID:      corpID,
		agentSecret: agentSecret,
		agentID:     agentID,
		httpC:       &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *wecomClient) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && time.Now().Before(c.tokenExp) {
		t := c.token
		c.mu.Unlock()
		return t, nil
	}
	c.mu.Unlock()

	u := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=%s&corpsecret=%s", c.corpID, c.agentSecret)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	resp, err := c.httpC.Do(req)
	if err != nil {
		return "", err
	}
	defer oklog.Close("wecom token response", resp.Body)

	var r struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if r.ErrCode != 0 {
		return "", fmt.Errorf("token err %d: %s", r.ErrCode, r.ErrMsg)
	}

	c.mu.Lock()
	c.token = r.AccessToken
	c.tokenExp = time.Now().Add(time.Duration(r.ExpiresIn) * time.Second).Add(-5 * time.Minute)
	t := c.token
	c.mu.Unlock()
	return t, nil
}

func (c *wecomClient) sendText(ctx context.Context, userID, text string) error {
	token, err := c.getToken(ctx)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"touser":  userID,
		"msgtype": "text",
		"agentid": c.agentID,
		"text":    map[string]string{"content": text},
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	u := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/message/send?access_token=%s", token)
	req, err := http.NewRequestWithContext(ctx, "POST", u, &buf)
	if err != nil {
		return fmt.Errorf("create send request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpC.Do(req)
	if err != nil {
		return err
	}
	defer oklog.Close("wecom send response", resp.Body)
	var r struct {
		ErrCode int `json:"errcode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("decode send response: %w", err)
	}
	if r.ErrCode != 0 {
		return fmt.Errorf("send err %d", r.ErrCode)
	}
	return nil
}

type wecomAdapter struct{ cli *wecomClient }

func (a *wecomAdapter) PlatformName() string { return "wecom" }
func (a *wecomAdapter) SendMessage(ctx context.Context, userID, text string) error {
	return a.cli.sendText(ctx, userID, text)
}
func (a *wecomAdapter) SendTyping(_ context.Context, _ string) {}

type server struct {
	core    *imbot.BotCore
	adapter *wecomAdapter
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var cb struct {
		FromUserName string `json:"FromUserName"`
		MsgType      string `json:"MsgType"`
		Content      string `json:"Content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&cb); err != nil {
		log.Printf("decode callback: %v", err)
		if err := json.NewEncoder(w).Encode(map[string]int{"errcode": 0}); err != nil {
			log.Printf("write response: %v", err)
		}
		return
	}
	if cb.MsgType != "text" || strings.TrimSpace(cb.Content) == "" {
		if err := json.NewEncoder(w).Encode(map[string]int{"errcode": 0}); err != nil {
			log.Printf("write response: %v", err)
		}
		return
	}
	userID, text := cb.FromUserName, strings.TrimSpace(cb.Content)
	ctx := r.Context()

	switch {
	case text == "/start":
		if err := s.adapter.SendMessage(ctx, userID, "Hello! I am OK Agent Bot. Send a message to chat. Send /new to start a new conversation."); err != nil {
			log.Printf("send start: %v", err)
		}
		if err := json.NewEncoder(w).Encode(map[string]int{"errcode": 0}); err != nil {
			log.Printf("write response: %v", err)
		}
		return
	case text == "/new":
		s.core.Sessions.Remove(userID)
		if err := s.adapter.SendMessage(ctx, userID, "New conversation started."); err != nil {
			log.Printf("send new: %v", err)
		}
		if err := json.NewEncoder(w).Encode(map[string]int{"errcode": 0}); err != nil {
			log.Printf("write response: %v", err)
		}
		return
	}

	// MUST use context.Background() — the HTTP handler returns immediately and
	// r.Context() is cancelled, which would kill the ACP subprocess created by
	// GetOrCreateSession (winhide.CommandContext binds the process to ctx).
	bg := context.Background()
	session := s.core.GetOrCreateSession(bg, userID, userID)
	if session == nil {
		if err := s.adapter.SendMessage(bg, userID, "Session creation failed."); err != nil {
			log.Printf("send session failed: %v", err)
		}
		if err := json.NewEncoder(w).Encode(map[string]int{"errcode": 0}); err != nil {
			log.Printf("write response: %v", err)
		}
		return
	}
	go s.core.SendPrompt(bg, session, text)
	if err := json.NewEncoder(w).Encode(map[string]int{"errcode": 0}); err != nil {
		log.Printf("write response: %v", err)
	}
}

func main() {
	if _, err := config.Load(); err != nil {
		log.Printf("wechat: config.Load: %v", err)
	}

	corpID := flag.String("corp-id", os.Getenv("WECOM_CORP_ID"), "corp id")
	agentSecret := flag.String("agent-secret", os.Getenv("WECOM_AGENT_SECRET"), "agent secret")
	agentID := flag.String("agent-id", os.Getenv("WECOM_AGENT_ID"), "agent id")
	okBin := flag.String("ok-bin", findOK(), "ok binary path")
	okModel := flag.String("ok-model", "", "model name")
	workDir := flag.String("work-dir", ".", "working directory")
	listen := flag.String("listen", ":8080", "http listen address")
	flag.Parse()

	if *corpID == "" || *agentSecret == "" || *agentID == "" {
		log.Fatal("WECOM_CORP_ID, WECOM_AGENT_SECRET, WECOM_AGENT_ID required")
	}
	if _, err := os.Stat(*okBin); err != nil {
		log.Printf("feishu: ok binary not found at %s, will rely on PATH", *okBin)
		*okBin = "ok"
	}

	a := &wecomAdapter{cli: newWecomClient(*corpID, *agentSecret, *agentID)}
	core := imbot.NewBotCore(a, *okBin, *okModel, *workDir)
	srv := &server{core: core, adapter: a}

	http.Handle("/callback", srv)
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("ok")); err != nil {
			log.Printf("health write: %v", err)
		}
	})

	log.Printf("wecom bot listening on %s (ok: %s)", *listen, *okBin)
	if err := http.ListenAndServe(*listen, nil); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

func findOK() string {
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	candidates := []string{
		filepath.Join(exeDir, "..", "ok.exe"),
		filepath.Join(exeDir, "..", "..", "ok.exe"),
		filepath.Join(exeDir, "ok.exe"),
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
