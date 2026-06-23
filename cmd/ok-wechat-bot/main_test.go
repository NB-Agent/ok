package main

import (
	"context"
	"testing"
	"time"
)

func TestNewWecomClient(t *testing.T) {
	c := newWecomClient("corpid", "secret", "10001")
	if c.corpID != "corpid" {
		t.Errorf("corpID = %q", c.corpID)
	}
	if c.agentSecret != "secret" {
		t.Errorf("agentSecret = %q", c.agentSecret)
	}
	if c.agentID != "10001" {
		t.Errorf("agentID = %q", c.agentID)
	}
	if c.httpC.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", c.httpC.Timeout)
	}
}

func TestWecom_PlatformName(t *testing.T) {
	c := newWecomClient("id", "secret", "1")
	a := &wecomAdapter{cli: c}
	if got := a.PlatformName(); got != "wecom" {
		t.Errorf("PlatformName = %q, want %q", got, "wecom")
	}
}

func TestWecom_AdapterSendMessage(t *testing.T) {
	c := newWecomClient("id", "secret", "1")
	a := &wecomAdapter{cli: c}
	// This will try to make an HTTP call, which will fail
	// But we want to verify the adapter plumbing works
	err := a.SendMessage(context.Background(), "user", "hello")
	if err == nil {
		t.Fatal("expected error (no network)")
	}
}

func TestWecom_TokenCaching(t *testing.T) {
	c := newWecomClient("id", "secret", "1")

	// Initially no token — getToken should try the API
	_, err := c.getToken(context.Background())
	if err == nil {
		t.Fatal("expected error (no network)")
	}

	// Manually set a valid token, should use cached version
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
