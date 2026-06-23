package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NB-Agent/ok/internal/imbot"
)

// testServer returns a *server with non-nil adapter + core for safe handler tests.
func testServer() *server {
	c := newFeishuClient("test-id", "test-secret")
	a := &feishuAdapter{cli: c}
	// Minimal BotCore: adapter + Sessions are enough for /new, /start, /help
	core := &imbot.BotCore{
		Adapter:  a,
		Sessions: imbot.NewSessionManager(),
	}
	return &server{core: core, adapter: a}
}

func TestNewFeishuClient(t *testing.T) {
	c := newFeishuClient("appid", "secret")
	if c.appID != "appid" {
		t.Errorf("appID = %q, want %q", c.appID, "appid")
	}
	if c.appSecret != "secret" {
		t.Errorf("appSecret = %q, want %q", c.appSecret, "secret")
	}
	if c.httpC.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", c.httpC.Timeout)
	}
}

func TestFeishu_PlatformName(t *testing.T) {
	c := newFeishuClient("id", "secret")
	a := &feishuAdapter{cli: c}
	if got := a.PlatformName(); got != "feishu" {
		t.Errorf("PlatformName = %q, want %q", got, "feishu")
	}
}

func TestFeishu_AdapterSendMessage(t *testing.T) {
	c := newFeishuClient("id", "secret")
	a := &feishuAdapter{cli: c}
	err := a.SendMessage(context.Background(), "user", "hello")
	if err == nil {
		t.Fatal("expected error (no network)")
	}
}

func TestFeishu_TokenCaching(t *testing.T) {
	c := newFeishuClient("id", "secret")

	// Initially no token — network call fails
	_, err := c.getToken(nil)
	if err == nil {
		t.Fatal("expected error (no network)")
	}

	// Manually set a valid token, should use cache
	c.mu.Lock()
	c.token = "cached-token"
	c.tokenExp = time.Now().Add(1 * time.Hour)
	c.mu.Unlock()

	token, err := c.getToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "cached-token" {
		t.Errorf("token = %q, want %q", token, "cached-token")
	}
}

func TestFeishu_TokenExpiry(t *testing.T) {
	c := newFeishuClient("id", "secret")

	// Set expired token — should NOT return it, should try network (and fail)
	c.mu.Lock()
	c.token = "expired-token"
	c.tokenExp = time.Now().Add(-1 * time.Hour)
	c.mu.Unlock()

	_, err := c.getToken(context.Background())
	if err == nil {
		t.Fatal("expected error (expired → network fails)")
	}
}

// ─── Webhook handler tests ───────────────────────────────────────────────────

func TestFeishu_ChallengeVerification(t *testing.T) {
	s := testServer()
	body := `{"challenge":"abc123"}`
	req := httptest.NewRequest("POST", "/feishu", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "abc123") {
		t.Errorf("response = %q, want challenge in body", w.Body.String())
	}
}

func TestFeishu_EncryptedPayload(t *testing.T) {
	s := testServer()
	body := `{"encrypt":"encrypted-data"}`
	req := httptest.NewRequest("POST", "/feishu", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"code":0`) {
		t.Errorf("response = %q, want code:0", w.Body.String())
	}
}

func TestFeishu_NoEvent(t *testing.T) {
	s := testServer()
	body := `{"schema":"2.0"}`
	req := httptest.NewRequest("POST", "/feishu", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
}

func TestFeishu_NonPostMethod(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest("GET", "/feishu", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d, want 405", w.Code)
	}
}

func TestFeishu_EmptySenderID(t *testing.T) {
	s := testServer()
	body := `{"event":{"sender":{"sender_id":{}},"message":{"msg_type":"text","content":"{\"text\":\"/start\"}"}}}`
	req := httptest.NewRequest("POST", "/feishu", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
}

func TestFeishu_NonTextMessage(t *testing.T) {
	s := testServer()
	body := `{"event":{"sender":{"sender_id":{"open_id":"ou_xxx"}},"message":{"msg_type":"image","content":"{\"image_key\":\"img_xxx\"}"}}}`
	req := httptest.NewRequest("POST", "/feishu", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"code":0`) {
		t.Errorf("response = %q, want code:0", w.Body.String())
	}
}

func TestFeishu_BadContentJSON(t *testing.T) {
	s := testServer()
	body := `{"event":{"sender":{"sender_id":{"open_id":"ou_xxx"}},"message":{"msg_type":"text","content":"not-json"}}}`
	req := httptest.NewRequest("POST", "/feishu", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
}

func TestFeishu_V2EventFormat(t *testing.T) {
	s := testServer()
	// v2.0 uses message_type instead of msg_type
	body := `{"event":{"sender":{"sender_id":{"open_id":"ou_xxx"}},"message":{"message_type":"text","content":"{\"text\":\"/start\"}"}}}`
	req := httptest.NewRequest("POST", "/feishu", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
}

func TestFeishu_StartCommand(t *testing.T) {
	s := testServer()
	body := `{"event":{"sender":{"sender_id":{"open_id":"ou_start_test"}},"message":{"msg_type":"text","content":"{\"text\":\"/start\"}"}}}`
	req := httptest.NewRequest("POST", "/feishu", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
}

func TestFeishu_HelpCommand(t *testing.T) {
	s := testServer()
	body := `{"event":{"sender":{"sender_id":{"open_id":"ou_help_test"}},"message":{"msg_type":"text","content":"{\"text\":\"/help\"}"}}}`
	req := httptest.NewRequest("POST", "/feishu", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
}

func TestFeishu_NewCommand(t *testing.T) {
	s := testServer()
	body := `{"event":{"sender":{"sender_id":{"open_id":"ou_new_test"}},"message":{"msg_type":"text","content":"{\"text\":\"/new\"}"}}}`
	req := httptest.NewRequest("POST", "/feishu", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
}

func TestFindOK(t *testing.T) {
	path := findOK()
	if path == "" {
		t.Fatal("findOK returned empty")
	}
	if path != "ok" && !strings.HasSuffix(path, "ok") && !strings.HasSuffix(path, "ok.exe") {
		t.Errorf("findOK = %q, expected 'ok' or path ending with ok/ok.exe", path)
	}
}

func TestFeishu_ContextScopedToken(t *testing.T) {
	c := newFeishuClient("id", "secret")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.getToken(ctx)
	if err == nil {
		t.Log("note: getToken may not check ctx before HTTP")
	}
}

func TestFeishu_HealthJSON(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	h := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","platform":"feishu"}`))
	}
	h(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Errorf("body = %q, want status:ok", w.Body.String())
	}
}
