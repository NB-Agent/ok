package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewDingtalkAdapter(t *testing.T) {
	a := newDingtalkAdapter("https://oapi.dingtalk.com/robot/send?access_token=test")
	if a.webhookURL != "https://oapi.dingtalk.com/robot/send?access_token=test" {
		t.Errorf("webhookURL = %q", a.webhookURL)
	}
	if a.client.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", a.client.Timeout)
	}
}

func TestDingtalk_PlatformName(t *testing.T) {
	a := newDingtalkAdapter("url")
	if got := a.PlatformName(); got != "dingtalk" {
		t.Errorf("PlatformName = %q, want %q", got, "dingtalk")
	}
}

func TestDingtalk_Healthz(t *testing.T) {
	b := newBot("url", "ok", "", ".")
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	b.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDingtalk_EmptyBody(t *testing.T) {
	b := newBot("url", "ok", "", ".")
	req := httptest.NewRequest("POST", "/dingtalk", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	b.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDingtalk_TextMessage(t *testing.T) {
	b := newBot("url", "ok", "", ".")
	payload := map[string]any{
		"senderStaffId": "user123",
		"senderNick":    "Alice",
		"msgType":       "text",
		"text":          map[string]string{"content": "hello"},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/dingtalk", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	b.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDingtalk_NonTextMessage(t *testing.T) {
	b := newBot("url", "ok", "", ".")
	payload := map[string]any{
		"msgType": "image",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/dingtalk", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	b.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
