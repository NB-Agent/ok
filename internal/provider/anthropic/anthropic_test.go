package anthropic

import (
	"testing"

	"github.com/NB-Agent/ok/internal/provider"
)

func TestNew_ValidConfig(t *testing.T) {
	p, err := New(provider.Config{
		Name:    "test",
		BaseURL: "https://api.anthropic.com",
		Model:   "claude-sonnet-4-20250514",
		APIKey:  "sk-test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

func TestNew_MissingBaseURL(t *testing.T) {
	_, err := New(provider.Config{
		Name:  "test",
		Model: "claude-sonnet-4-20250514",
	})
	if err == nil {
		t.Fatal("expected error for missing base_url, got nil")
	}
}

func TestNew_MissingModel(t *testing.T) {
	_, err := New(provider.Config{
		Name:    "test",
		BaseURL: "https://api.anthropic.com",
	})
	if err == nil {
		t.Fatal("expected error for missing model, got nil")
	}
}

func TestNew_DefaultsName(t *testing.T) {
	p, err := New(provider.Config{
		BaseURL: "https://api.anthropic.com",
		Model:   "claude-sonnet-4-20250514",
		APIKey:  "sk-test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}
