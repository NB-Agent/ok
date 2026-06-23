package imbot

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionManager_CRUD(t *testing.T) {
	sm := NewSessionManager()

	// Initially empty
	if s := sm.Get("user1"); s != nil {
		t.Fatal("expected nil for missing user")
	}

	// Add
	s1 := &Session{ID: "sess1", UserID: "user1", UserName: "Alice"}
	sm.Add("user1", s1)

	if got := sm.Get("user1"); got != s1 {
		t.Fatal("Get returned wrong session")
	}
	if got := sm.GetByACP("sess1"); got != s1 {
		t.Fatal("GetByACP returned wrong session")
	}

	// Add second
	s2 := &Session{ID: "sess2", UserID: "user2", UserName: "Bob"}
	sm.Add("user2", s2)

	// Remove
	sm.Remove("user1")
	if s := sm.Get("user1"); s != nil {
		t.Fatal("expected nil after Remove")
	}
	if s := sm.GetByACP("sess1"); s != nil {
		t.Fatal("expected nil GetByACP after Remove")
	}
	// user2 should still exist
	if s := sm.Get("user2"); s != s2 {
		t.Fatal("user2 should still exist after removing user1")
	}
}

func TestSessionManager_ShutdownAll(t *testing.T) {
	sm := NewSessionManager()

	// Sessions without Client should not panic
	s1 := &Session{ID: "s1", UserID: "u1"}
	s2 := &Session{ID: "s2", UserID: "u2", Client: &ACPClient{}}
	sm.Add("u1", s1)
	sm.Add("u2", s2)

	// Should not deadlock or panic
	done := make(chan struct{})
	go func() {
		sm.ShutdownAll()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ShutdownAll deadlocked or timed out")
	}
}

func TestSessionManager_Concurrent(t *testing.T) {
	sm := NewSessionManager()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			uid := strings.TrimSpace(string(rune('a' + n%26)))
			s := &Session{ID: uid, UserID: uid}
			sm.Add(uid, s)
			sm.Get(uid)
			sm.Remove(uid)
		}(i)
	}
	wg.Wait()
}

func TestSessionManager_RemoveNonExistent(t *testing.T) {
	sm := NewSessionManager()
	sm.Remove("nonexistent") // should not panic
}

func TestNewBotCore(t *testing.T) {
	a := &mockAdapter{}
	bc := NewBotCore(a, "/usr/bin/ok", "deepseek", "/tmp/work")
	if bc.OKBin != "/usr/bin/ok" {
		t.Errorf("OKBin = %q", bc.OKBin)
	}
	if bc.OKModel != "deepseek" {
		t.Errorf("OKModel = %q", bc.OKModel)
	}
	if bc.WorkDir != "/tmp/work" {
		t.Errorf("WorkDir = %q", bc.WorkDir)
	}
	if bc.Sessions == nil {
		t.Fatal("Sessions not initialized")
	}
}

func TestShouldFlush(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"", false},
		{"hello", false},
		{"hello world.", true},
		{"hello world!", true},
		{"hello world?", true},
		{"hello\n", true},
		{strings.Repeat("a", 2001), true},
		{strings.Repeat("a", 2000), false},
		{"no punctuation here", false},
	}
	for _, tc := range tests {
		got := shouldFlush(tc.s)
		if got != tc.want {
			t.Errorf("shouldFlush(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

func TestFlushBuffer_Empty(t *testing.T) {
	bc := NewBotCore(&mockAdapter{}, "ok", "", ".")
	s := &Session{Buf: strings.Builder{}, UserID: "test"}
	bc.flushBuffer(s)
	// Should not panic — that's the test
}

func TestFlushBuffer_ShortText(t *testing.T) {
	bc := NewBotCore(&mockAdapter{}, "ok", "", ".")
	s := &Session{Buf: strings.Builder{}, UserID: "test"}
	s.Buf.WriteString("hello")
	bc.flushBuffer(s)
	// Should have sent "hello"
}

// TestACPFrames tests JSON-RPC frame encoding structure
func TestACPFrames(t *testing.T) {
	notif := ACPFrame{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  json.RawMessage(`{"sessionId":"s1","update":{}}`),
	}
	b, err := json.Marshal(notif)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded ACPFrame
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Method != "session/update" || decoded.JSONRPC != "2.0" {
		t.Errorf("bad decode: %+v", decoded)
	}
}

// TestNewACPClientInstantiation tests that ACPClient struct is valid
func TestNewACPClientInstantiation(t *testing.T) {
	ac := &ACPClient{
		nextID:  1,
		pending: make(map[string]chan<- json.RawMessage),
		notifH:  make(map[string]func(json.RawMessage)),
	}
	if ac.nextID != 1 {
		t.Errorf("nextID = %d", ac.nextID)
	}
	ac.Close() // should not panic (no cmd set)
}

// ─── mock adapter ───

type mockAdapter struct {
	lastMsg string
	mu      sync.Mutex
}

func (m *mockAdapter) PlatformName() string { return "mock" }
func (m *mockAdapter) SendMessage(_ context.Context, _, text string) error {
	m.mu.Lock()
	m.lastMsg = text
	m.mu.Unlock()
	return nil
}
func (m *mockAdapter) SendTyping(_ context.Context, _ string) {}
