package bridge

import (
	"context"
	"testing"
	"time"
)

func TestNewBridge(t *testing.T) {
	ch := make(chan string, 1)
	br := NewBridge("test-secret", 9463, func(ctx context.Context, task string) (<-chan string, error) {
		ch <- task
		return ch, nil
	})
	if br == nil {
		t.Fatal("NewBridge returned nil")
	}
	if br.port <= 0 {
		t.Error("port should be positive")
	}
}

func TestBridgeStartStop(t *testing.T) {
	ch := make(chan string, 1)
	br := NewBridge("test-secret-2", 0, func(ctx context.Context, task string) (<-chan string, error) {
		return ch, nil
	})
	if err := br.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	br.Stop()
}

func TestSecretID(t *testing.T) {
	br := NewBridge("hello", 0, nil)
	id := br.secretID()
	if len(id) == 0 {
		t.Error("secretID returned empty")
	}
	if id[:3] != "ok-" {
		t.Errorf("secretID should start with 'ok-', got %q", id)
	}
}

func TestLoadOrCreateSecret(t *testing.T) {
	secret := LoadOrCreateSecret()
	if len(secret) < 16 {
		t.Errorf("secret too short: %d bytes", len(secret))
	}
}

func TestPeersEmpty(t *testing.T) {
	br := NewBridge("test", 0, nil)
	peers := br.Peers()
	if len(peers) != 0 {
		t.Errorf("expected 0 peers, got %d", len(peers))
	}
}
