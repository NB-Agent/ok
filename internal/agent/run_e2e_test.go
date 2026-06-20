package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// TestRunFullLoopFinalAnswer verifies the agent can produce a final text answer.
func TestRunFullLoopFinalAnswer(t *testing.T) {
	prov := &mockProvider{name: "p", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "The answer is 42."},
		{Type: provider.ChunkDone},
	}}
	reg := tool.NewRegistry()

	var got []event.Event
	sink := event.FuncSink(func(e *event.Event) { got = append(got, *e) })
	a := New(prov, reg, NewSession(""), Options{MaxSteps: 5}, sink)

	err := a.Run(context.Background(), "what is the answer?")
	// May return nil (model answered) or max_steps error (mock replays chunks forever).
	// Either way, the session should have messages accumulated.
	_ = err

	if !hasKind(got, event.Message) {
		t.Error("missing Message event (final answer)")
	}
}

// TestRunFullLoopWithTools verifies tool dispatch and result events in a full turn.
func TestRunFullLoopWithTools(t *testing.T) {
	prov := &mockProvider{name: "p", chunks: []provider.Chunk{
		{Type: provider.ChunkToolCallStart, ToolCall: &provider.ToolCall{ID: "c1", Name: "r"}},
		{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "c1", Name: "r", Arguments: `{}`}},
		{Type: provider.ChunkToolCallStart, ToolCall: &provider.ToolCall{ID: "c2", Name: "r"}},
		{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "c2", Name: "r", Arguments: `{}`}},
		{Type: provider.ChunkDone},
	}}
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "r", readOnly: true})

	var got []event.Event
	var mu sync.Mutex
	sink := event.FuncSink(func(e *event.Event) {
		mu.Lock()
		got = append(got, *e)
		mu.Unlock()
	})
	a := New(prov, reg, NewSession(""), Options{MaxSteps: 2}, sink)
	_ = a.Run(context.Background(), "run two reads")

	mu.Lock()
	defer mu.Unlock()

	disp := countKind(got, event.ToolDispatch)
	if disp < 2 {
		t.Errorf("want >= 2 ToolDispatch events, got %d", disp)
	}
	res := countKind(got, event.ToolResult)
	if res < 2 {
		t.Errorf("want >= 2 ToolResult events, got %d", res)
	}
}

// TestRunSessionGrows verifies the session accumulates messages each turn.
func TestRunSessionGrows(t *testing.T) {
	prov := &mockProvider{name: "p", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "Hello back."},
		{Type: provider.ChunkDone},
	}}
	reg := tool.NewRegistry()
	sess := NewSession("You are helpful.")
	a := New(prov, reg, sess, Options{MaxSteps: 2}, event.Discard)

	_ = a.Run(context.Background(), "hello")

	if len(sess.Messages) < 1 {
		t.Error("session should have messages after Run")
	}
	hasUser := false
	hasAssistant := false
	for _, m := range sess.Messages {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, "hello") {
			hasUser = true
		}
		if m.Role == provider.RoleAssistant {
			hasAssistant = true
		}
	}
	if !hasUser {
		t.Error("session missing user message")
	}
	if !hasAssistant {
		t.Error("session missing assistant message")
	}
}

// TestStreamRetryOnError verifies the retry logic in stream() on ChunkError.
func TestStreamRetryOnError(t *testing.T) {
	prov := &mockProvider{name: "p", chunks: []provider.Chunk{
		{Type: provider.ChunkError, Err: fmt.Errorf("transient: 503 overloaded")},
	}}
	reg := tool.NewRegistry()
	a := New(prov, reg, NewSession(""), Options{MaxSteps: 1}, event.Discard)

	// Should retry up to maxStreamRetries (2) then fail.
	err := a.Run(context.Background(), "test")
	if err == nil {
		t.Error("expected error from persistent stream failure")
	}
}

// TestOnTurnCompleteFires verifies the callback fires after a successful turn.
func TestOnTurnCompleteFires(t *testing.T) {
	prov := &mockProvider{name: "p", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "done"},
		{Type: provider.ChunkDone},
	}}
	reg := tool.NewRegistry()

	var (
		mu       sync.Mutex
		fired    bool
		gotInput string
	)
	a := New(prov, reg, NewSession(""), Options{MaxSteps: 2}, event.Discard)
	a.SetOnTurnComplete(func(ctx context.Context, input, answer string) {
		mu.Lock()
		fired = true
		gotInput = input
		mu.Unlock()
	})

	_ = a.Run(context.Background(), "test input")

	mu.Lock()
	if !fired {
		t.Error("OnTurnComplete callback did not fire")
	}
	if gotInput != "test input" {
		t.Errorf("OnTurnComplete input = %q, want %q", gotInput, "test input")
	}
	mu.Unlock()
}

// TestUsageRecording verifies usage events are emitted correctly.
func TestUsageRecording(t *testing.T) {
	prov := &mockProvider{name: "p", chunks: []provider.Chunk{
		{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}
	reg := tool.NewRegistry()

	var gotUsage bool
	sink := event.FuncSink(func(e *event.Event) {
		if e.Kind == event.Usage {
			gotUsage = true
			if e.Usage.TotalTokens != 15 {
				t.Errorf("usage TotalTokens = %d, want 15", e.Usage.TotalTokens)
			}
		}
	})
	a := New(prov, reg, NewSession(""), Options{MaxSteps: 2}, sink)
	_ = a.Run(context.Background(), "hi")

	if !gotUsage {
		t.Error("no Usage event emitted")
	}
}

// TestMaxStepsEnforced verifies maxSteps limits the loop.
func TestMaxStepsEnforced(t *testing.T) {
	prov := &mockProvider{name: "p", chunks: []provider.Chunk{
		{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "c1", Name: "r", Arguments: `{}`}},
		{Type: provider.ChunkDone},
	}}
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "r", readOnly: true})

	a := New(prov, reg, NewSession(""), Options{MaxSteps: 1}, event.Discard)
	err := a.Run(context.Background(), "test")

	if err == nil {
		t.Error("expected max_steps error after 1 tool round, got nil")
	}
	if !strings.Contains(err.Error(), "max_steps") {
		t.Errorf("expected max_steps in error, got: %v", err)
	}
}

// --- multi-agent end-to-end ---

// TestTeamEndToEnd verifies an orchestrator + 2 specialists can complete a
// simple analysis task in parallel.
func TestTeamEndToEnd(t *testing.T) {
	s1Prov := &mockProvider{name: "spec1", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "Code review: looks good, one suggestion about error handling."},
		{Type: provider.ChunkDone},
	}}
	s2Prov := &mockProvider{name: "spec2", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "Security review: no vulnerabilities found."},
		{Type: provider.ChunkDone},
	}}

	specialists := []Specialist{
		{Name: "code-reviewer", Description: "Reviews code", Model: "m", Prov: s1Prov, ContextWindow: 8000, Prompt: "You review code."},
		{Name: "security-reviewer", Description: "Reviews security", Model: "m", Prov: s2Prov, ContextWindow: 8000, Prompt: "You review security."},
	}

	var events []event.Event
	sink := event.FuncSink(func(e *event.Event) { events = append(events, *e) })

	tm := NewTeam(nil, specialists, sink)

	results, err := tm.RunParallel(context.Background(), "review func main()", nil)
	if err != nil {
		t.Fatal("RunParallel failed:", err)
	}

	if len(results) != 2 {
		t.Errorf("want 2 results from RunParallel, got %d", len(results))
	}
	if r, ok := results["code-reviewer"]; !ok || !strings.Contains(r, "error handling") {
		t.Errorf("code-reviewer result: %q", r)
	}
	if r, ok := results["security-reviewer"]; !ok || !strings.Contains(r, "vulnerabilities") {
		t.Errorf("security-reviewer result: %q", r)
	}
}

// TestTeamEndToEndWithDelegateAll verifies the delegate_all tool.
func TestTeamEndToEndWithDelegateAll(t *testing.T) {
	s1Prov := &mockProvider{name: "spec1", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "Analysis A complete."},
		{Type: provider.ChunkDone},
	}}
	s2Prov := &mockProvider{name: "spec2", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "Analysis B complete."},
		{Type: provider.ChunkDone},
	}}

	specialists := []Specialist{
		{Name: "analyst-a", Description: "A", Model: "m", Prov: s1Prov, ContextWindow: 8000, Prompt: "p"},
		{Name: "analyst-b", Description: "B", Model: "m", Prov: s2Prov, ContextWindow: 8000, Prompt: "p"},
	}

	var events []event.Event
	sink := event.FuncSink(func(e *event.Event) { events = append(events, *e) })

	tm := NewTeam(nil, specialists, sink)

	dt := &parallelDelegateTool{team: tm}
	out, err := dt.Execute(context.Background(), mustMarshalRaw(t, map[string]any{"task": "analyze"}))
	if err != nil {
		t.Fatal("delegate_all failed:", err)
	}
	if !strings.Contains(out, "analyst-a") {
		t.Error("delegate_all output missing analyst-a section")
	}
	if !strings.Contains(out, "analyst-b") {
		t.Error("delegate_all output missing analyst-b section")
	}
	if !strings.Contains(out, "Analysis A") {
		t.Error("delegate_all output missing specialist A's result")
	}
	if !strings.Contains(out, "Analysis B") {
		t.Error("delegate_all output missing specialist B's result")
	}
}

// TestTeamOrchestratorDelegates verifies a full orchestrator → specialist flow.
func TestTeamOrchestratorDelegates(t *testing.T) {
	specProv := &mockProvider{name: "coder", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "Found: nil dereference on line 42."},
		{Type: provider.ChunkDone},
	}}

	// Orchestrator first calls delegate_coder, then returns a summary.
	orchProv := &mockProvider{name: "orch", chunks: []provider.Chunk{
		{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "d1", Name: "delegate_coder", Arguments: `{"task":"review the code"}`}},
		{Type: provider.ChunkDone},
	}}

	specialists := []Specialist{
		{Name: "coder", Description: "Code reviewer", Model: "m", Prov: specProv, ContextWindow: 8000, Prompt: "Review code."},
	}

	var events []event.Event
	sink := event.FuncSink(func(e *event.Event) { events = append(events, *e) })

	tm := NewTeam(nil, specialists, sink)

	orchReg := tool.NewRegistry()
	for _, name := range tm.tools.Names() {
		if tl, ok := tm.tools.Get(name); ok {
			orchReg.Add(tl)
		}
	}
	orchSess := NewSession("You lead a team. Use delegate_coder to ask the coder to review code.")
	orchestrator := New(orchProv, orchReg, orchSess, Options{MaxSteps: 5}, sink)
	tm.Orchestrator = orchestrator

	err := tm.Run(context.Background(), "review the codebase")
	if err != nil {
		t.Log("orchestrator Run:", err)
	}

	// Verify the orchestrator session has tool results from the delegate.
	hasToolResult := false
	for _, m := range orchestrator.Session().Messages {
		if m.Role == provider.RoleTool {
			hasToolResult = true
			break
		}
	}
	if !hasToolResult {
		t.Error("orchestrator session missing tool result from specialist delegation")
	}
}

// --- helpers ---

func hasKind(events []event.Event, kind event.Kind) bool {
	for _, e := range events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func countKind(events []event.Event, kind event.Kind) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func mustMarshalRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal("marshal:", err)
	}
	return b
}
