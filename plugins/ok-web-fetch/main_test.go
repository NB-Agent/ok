package main

import (
	"context"
	"testing"

	"github.com/NB-Agent/ok/internal/plugin"
)

func TestInfo(t *testing.T) {
	var s server
	name, ver := s.Info()
	if name != "ok-web-fetch" {
		t.Errorf("name = %q, want ok-web-fetch", name)
	}
	if ver != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", ver)
	}
}

func TestTools(t *testing.T) {
	var s server
	tools := s.Tools()
	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}
	for _, tool := range tools {
		if tool.Name == "" {
			t.Error("tool with empty name found")
		}
	}
}

func TestCallUnknownTool(t *testing.T) {
	var s server
	_, err := s.Call(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestCallMissingURL(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{})
	_, err := s.Call(context.Background(), "web_fetch", args)
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

func TestCallBadURL(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{"url": "not-a-url"})
	_, err := s.Call(context.Background(), "web_fetch", args)
	if err == nil {
		t.Fatal("expected error for bad url")
	}
}

func TestSSRFBlocked(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{"url": "http://10.0.0.1/"})
	_, err := s.Call(context.Background(), "web_fetch", args)
	if err == nil {
		t.Fatal("expected SSRF block for private IP")
	}
}

func TestSSRFLoopback(t *testing.T) {
	var s server
	args := plugin.MustJSON(map[string]string{"url": "http://127.0.0.1:9999/"})
	_, err := s.Call(context.Background(), "web_fetch", args)
	if err == nil {
		t.Fatal("expected connection refused or timeout, got nil error")
	}
	// loopback is allowed but nothing is listening, so we expect error
}
