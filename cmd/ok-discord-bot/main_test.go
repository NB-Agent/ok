package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewDiscordClient(t *testing.T) {
	c := newDiscordClient("test-token")
	if c.token != "test-token" {
		t.Errorf("token = %q, want %q", c.token, "test-token")
	}
	if c.apiURL != "https://discord.com/api/v10" {
		t.Errorf("apiURL = %q", c.apiURL)
	}
	if c.httpC.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", c.httpC.Timeout)
	}
}

func TestDiscordAdapter_PlatformName(t *testing.T) {
	c := newDiscordClient("tok")
	a := newDiscordAdapter(c)
	if got := a.PlatformName(); got != "discord" {
		t.Errorf("PlatformName = %q, want %q", got, "discord")
	}
}

func TestDiscordAdapter_SendMessage_BadUserID(t *testing.T) {
	c := newDiscordClient("tok")
	a := newDiscordAdapter(c)
	err := a.SendMessage(context.Background(), "invalid", "hello")
	if err == nil {
		t.Fatal("expected error for bad userID")
	}
	if !strings.Contains(err.Error(), "bad userID") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVerifyDiscordReq_Empty(t *testing.T) {
	r, _ := http.NewRequest("POST", "/", nil)
	if verifyDiscordReq("abc", r, nil) {
		t.Error("expected false for empty request")
	}
}

func TestVerifyDiscordReq_MissingHeaders(t *testing.T) {
	r, _ := http.NewRequest("POST", "/", nil)
	r.Header.Set("X-Signature-Ed25519", "abcd")
	// missing X-Signature-Timestamp
	if verifyDiscordReq("abc", r, []byte("body")) {
		t.Error("expected false when timestamp missing")
	}
}

func TestDiscordAdapter_DMCache(t *testing.T) {
	c := newDiscordClient("tok")
	a := newDiscordAdapter(c)
	// user: prefix should try DM lookup — user:testuser will fail since
	// createDM makes a real HTTP call we can't make in unit tests.
	err := a.SendMessage(context.Background(), "user:testuser", "hello")
	if err == nil {
		t.Fatal("expected error (cannot create DM in unit test)")
	}
	// But the error should be from createDM, not from a bad userID format
	if strings.Contains(err.Error(), "bad userID") {
		t.Errorf("user: prefix should be valid, got: %v", err)
	}
}
