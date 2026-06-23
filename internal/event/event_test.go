package event

import (
	"sync"
	"testing"

	"github.com/NB-Agent/ok/internal/provider"
)

func TestFuncSink(t *testing.T) {
	var received []Event
	s := FuncSink(func(e *Event) {
		received = append(received, *e)
	})

	s.Emit(&Event{Kind: TurnStarted})
	s.Emit(&Event{Kind: Text, Text: "hello"})
	s.Emit(&Event{Kind: TurnDone, Err: nil})

	if len(received) != 3 {
		t.Fatalf("expected 3 events, got %d", len(received))
	}
	if received[0].Kind != TurnStarted {
		t.Errorf("event 0 kind = %d", received[0].Kind)
	}
	if received[1].Kind != Text || received[1].Text != "hello" {
		t.Errorf("event 1 = %+v", received[1])
	}
}

func TestDiscard(t *testing.T) {
	// Should not panic
	Discard.Emit(&Event{Kind: TurnStarted})
	Discard.Emit(&Event{Kind: Text, Text: "ignored"})
}

func TestSyncNil(t *testing.T) {
	// Sync(nil) returns Discard — calling Emit must not panic.
	s := Sync(nil)
	s.Emit(&Event{Kind: TurnStarted})
	s.Emit(&Event{Kind: Text, Text: "should not panic"})
}

func TestSyncSerialization(t *testing.T) {
	var mu sync.Mutex
	var order []int

	s := Sync(FuncSink(func(e *Event) {
		mu.Lock()
		order = append(order, len(order))
		mu.Unlock()
	}))

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			s.Emit(&Event{Kind: Text})
		}()
	}
	wg.Wait()

	if len(order) != n {
		t.Errorf("expected %d events, got %d", n, len(order))
	}
}

func TestEventKindValues(t *testing.T) {
	_ = TurnStarted
	_ = Reasoning
	_ = Text
	_ = Message
	_ = ToolDispatch
	_ = ToolResult
	_ = Usage
	_ = Notice
	_ = Phase
	_ = ApprovalRequest
	_ = AskRequest
	_ = TurnDone
}

func TestToolFields(t *testing.T) {
	tl := Tool{
		ID:       "call-1",
		Name:     "read_file",
		Args:     `{"path":"x.go"}`,
		Output:   "content",
		ReadOnly: true,
		ParentID: "parent-0",
		Partial:  true,
	}
	if tl.ID != "call-1" {
		t.Errorf("ID = %q", tl.ID)
	}
	if !tl.ReadOnly {
		t.Error("ReadOnly should be true")
	}
	if tl.ParentID != "parent-0" {
		t.Errorf("ParentID = %q", tl.ParentID)
	}
	if !tl.Partial {
		t.Error("Partial should be true")
	}
}

func TestApprovalFields(t *testing.T) {
	a := Approval{
		ID:      "approval-1",
		Tool:    "bash",
		Subject: "rm -rf /tmp/test",
	}
	if a.ID != "approval-1" || a.Tool != "bash" {
		t.Errorf("approval = %+v", a)
	}
}

func TestAskFields(t *testing.T) {
	aq := Ask{
		ID: "ask-1",
		Questions: []AskQuestion{
			{
				ID:      "q1",
				Header:  "Language",
				Prompt:  "Which language?",
				Options: []AskOption{{Label: "Go"}, {Label: "Rust", Description: "via CGo"}},
				Multi:   false,
			},
		},
	}
	if aq.ID != "ask-1" {
		t.Errorf("Ask.ID = %q", aq.ID)
	}
	if len(aq.Questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(aq.Questions))
	}
	q := aq.Questions[0]
	if q.Header != "Language" {
		t.Errorf("header = %q", q.Header)
	}
	if len(q.Options) != 2 {
		t.Errorf("expected 2 options, got %d", len(q.Options))
	}
}

func TestAskAnswer(t *testing.T) {
	aa := AskAnswer{
		QuestionID: "q1",
		Selected:   []string{"Go", "Rust"},
	}
	if aa.QuestionID != "q1" {
		t.Errorf("QuestionID = %q", aa.QuestionID)
	}
	if len(aa.Selected) != 2 {
		t.Errorf("selected = %v", aa.Selected)
	}
}

func TestEventUsageFields(t *testing.T) {
	usage := &provider.Usage{
		PromptTokens:     100,
		CompletionTokens: 200,
	}
	evt := Event{
		Kind:        Usage,
		Usage:       usage,
		SessionHit:  50,
		SessionMiss: 10,
	}
	if evt.SessionHit != 50 || evt.SessionMiss != 10 {
		t.Errorf("session cache = %d/%d", evt.SessionHit, evt.SessionMiss)
	}
}

func TestLevelConstants(t *testing.T) {
	if LevelInfo != 0 {
		t.Error("LevelInfo should be 0 (zero value)")
	}
	if LevelWarn != 1 {
		t.Error("LevelWarn should be 1")
	}
}

func TestEventNotice(t *testing.T) {
	evt := Event{
		Kind:  Notice,
		Text:  "compaction complete",
		Level: LevelInfo,
	}
	if evt.Text != "compaction complete" || evt.Level != LevelInfo {
		t.Errorf("notice = %+v", evt)
	}
}

func TestEventTurnDone(t *testing.T) {
	evt := Event{Kind: TurnDone, Err: nil}
	if evt.Err != nil {
		t.Error("nil Err should be clean completion")
	}
}

func TestSyncNestedSink(t *testing.T) {
	var received []Event
	inner := FuncSink(func(e *Event) { received = append(received, *e) })
	s := Sync(inner)

	s.Emit(&Event{Kind: TurnStarted})
	s.Emit(&Event{Kind: Text, Text: "a"})

	if len(received) != 2 {
		t.Fatalf("expected 2 events, got %d", len(received))
	}
}

func BenchmarkFuncSink(b *testing.B) {
	s := FuncSink(func(*Event) {})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Emit(&Event{Kind: Text, Text: "bench"})
	}
}

func BenchmarkSyncSink(b *testing.B) {
	s := Sync(FuncSink(func(*Event) {}))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Emit(&Event{Kind: Text, Text: "bench"})
	}
}
