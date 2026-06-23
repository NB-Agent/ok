package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewWhatsAppAdapter(t *testing.T) {
	a := newWhatsAppAdapter("12345", "tok123")
	if a.phoneID != "12345" {
		t.Errorf("phoneID = %q", a.phoneID)
	}
	if a.token != "tok123" {
		t.Errorf("token = %q", a.token)
	}
	if a.apiBase != "https://graph.facebook.com/v22.0" {
		t.Errorf("apiBase = %q", a.apiBase)
	}
	if a.client.Timeout != 60*time.Second {
		t.Errorf("timeout = %v, want 60s", a.client.Timeout)
	}
}

func TestWhatsApp_PlatformName(t *testing.T) {
	a := newWhatsAppAdapter("1", "tok")
	if got := a.PlatformName(); got != "whatsapp" {
		t.Errorf("PlatformName = %q, want %q", got, "whatsapp")
	}
}

func TestWhatsApp_SendMessage_NoServer(t *testing.T) {
	// This should fail because no real WhatsApp server is running
	a := newWhatsAppAdapter("1", "tok")
	err := a.SendMessage(context.Background(), "user123", "hello")
	// Expected: some network error (connection refused or timeout)
	if err == nil {
		t.Fatal("expected error when no server available")
	}
}

func TestNewBot(t *testing.T) {
	b := newBot("1", "tok", "verify123", "ok", "", ".")
	if b.verify != "verify123" {
		t.Errorf("verify = %q", b.verify)
	}
	if b.wa.PlatformName() != "whatsapp" {
		t.Errorf("unexpected platform: %s", b.wa.PlatformName())
	}
}

func TestWebhookVerification(t *testing.T) {
	b := newBot("1", "tok", "myverify", "ok", "", ".")

	// Successful verification
	req := httptest.NewRequest("GET", "/webhook?hub.mode=subscribe&hub.verify_token=myverify&hub.challenge=123abc", nil)
	w := httptest.NewRecorder()
	mux := b.handler()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if body := strings.TrimSpace(w.Body.String()); body != "123abc" {
		t.Errorf("body = %q, want %q", body, "123abc")
	}

	// Failed verification (wrong token)
	req2 := httptest.NewRequest("GET", "/webhook?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=abc", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("wrong token: status = %d, want 403", w2.Code)
	}

	// Missing mode
	req3 := httptest.NewRequest("GET", "/webhook?hub.verify_token=myverify&hub.challenge=abc", nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	if w3.Code != http.StatusForbidden {
		t.Errorf("no mode: status = %d, want 403", w3.Code)
	}
}

func TestHealthz(t *testing.T) {
	b := newBot("1", "tok", "v", "ok", "", ".")
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	b.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
