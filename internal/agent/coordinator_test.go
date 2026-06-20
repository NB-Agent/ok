package agent

import (
	"context"
	"github.com/NB-Agent/ok/internal/event"
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// mockProvider replays preset chunks and records the last request it received.
type mockProvider struct {
	name    string
	chunks  []provider.Chunk
	lastReq provider.Request
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	m.lastReq = req
	ch := make(chan provider.Chunk, len(m.chunks))
	for _, c := range m.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func lastUser(req provider.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == provider.RoleUser {
			return req.Messages[i].Content
		}
	}
	return ""
}

// TestCoordinatorHandsPlanToExecutor checks the two-session handoff: the planner
// sees the raw task in its own session, and the executor receives the plan.
func TestCoordinatorHandsPlanToExecutor(t *testing.T) {
	planner := &mockProvider{name: "planner", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "1. read main.go\n2. fix the loop"},
		{Type: provider.ChunkDone},
	}}
	exec := &mockProvider{name: "executor", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "Done."},
		{Type: provider.ChunkDone},
	}}

	executor := New(exec, tool.NewRegistry(), NewSession("exec-sys"), Options{}, event.Discard)
	plannerSess := NewSession("planner-sys")
	coord := NewCoordinator(planner, plannerSess, nil, executor, "test-exec", 0, event.Discard)

	if err := coord.Run(context.Background(), "fix the bug"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := lastUser(planner.lastReq); !strings.Contains(got, "fix the bug") {
		t.Errorf("planner saw user %q, want it to contain the task", got)
	}
	if got := lastUser(exec.lastReq); !strings.Contains(got, "read main.go") || !strings.Contains(got, "fix the bug") {
		t.Errorf("executor saw user %q, want task + plan", got)
	}
	// planner session must accumulate (system, user, assistant-plan) so its
	// prefix grows prepend-only and stays cache-stable.
	if n := len(plannerSess.Messages); n != 3 {
		t.Errorf("planner session has %d messages, want 3", n)
	}
}

// TestCoordinatorPlannerErrorFallsBack verifies that when the planner's Stream
// returns an error, the executor still runs directly with the original task.
func TestCoordinatorPlannerErrorFallsBack(t *testing.T) {
	planner := &mockProvider{name: "planner", chunks: []provider.Chunk{
		{Type: provider.ChunkError, Err: context.DeadlineExceeded},
	}}
	exec := &mockProvider{name: "executor", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "Done."},
		{Type: provider.ChunkDone},
	}}

	executor := New(exec, tool.NewRegistry(), NewSession("exec-sys"), Options{}, event.Discard)
	plannerSess := NewSession("planner-sys")
	coord := NewCoordinator(planner, plannerSess, nil, executor, "test-exec", 0, event.Discard)

	if err := coord.Run(context.Background(), "fix the bug"); err != nil {
		t.Fatalf("Run should not fail after planner error fallback: %v", err)
	}
	// Executor must have received the raw task (no plan prepended).
	if got := lastUser(exec.lastReq); !strings.Contains(got, "fix the bug") {
		t.Errorf("executor should see raw task after planner failed, got %q", got)
	}
	if strings.Contains(lastUser(exec.lastReq), "planner-proposal") {
		t.Error("executor must NOT receive a plan block after planner failure")
	}
}

// TestCoordinatorTrivialPlanFallsBack verifies that a planner returning fewer
// than 5 words triggers the insufficient-plan guard → executor runs directly.
func TestCoordinatorTrivialPlanFallsBack(t *testing.T) {
	planner := &mockProvider{name: "planner", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}
	exec := &mockProvider{name: "executor", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "Done."},
		{Type: provider.ChunkDone},
	}}

	executor := New(exec, tool.NewRegistry(), NewSession("exec-sys"), Options{}, event.Discard)
	plannerSess := NewSession("planner-sys")
	coord := NewCoordinator(planner, plannerSess, nil, executor, "test-exec", 0, event.Discard)

	if err := coord.Run(context.Background(), "fix the bug"); err != nil {
		t.Fatalf("Run should not fail after trivial plan fallback: %v", err)
	}
	if got := lastUser(exec.lastReq); !strings.Contains(got, "fix the bug") {
		t.Errorf("executor should see raw task after trivial plan, got %q", got)
	}
	if strings.Contains(lastUser(exec.lastReq), "planner-proposal") {
		t.Error("executor must NOT receive a plan block for a trivial plan")
	}
}

// TestCoordinatorEmptyPlanFallsBack covers the empty-string guard path.
func TestCoordinatorEmptyPlanFallsBack(t *testing.T) {
	planner := &mockProvider{name: "planner", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "   "},
		{Type: provider.ChunkDone},
	}}
	exec := &mockProvider{name: "executor", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "Done."},
		{Type: provider.ChunkDone},
	}}

	executor := New(exec, tool.NewRegistry(), NewSession("exec-sys"), Options{}, event.Discard)
	plannerSess := NewSession("planner-sys")
	coord := NewCoordinator(planner, plannerSess, nil, executor, "test-exec", 0, event.Discard)

	if err := coord.Run(context.Background(), "task"); err != nil {
		t.Fatalf("Run should not fail after blank plan fallback: %v", err)
	}
	if got := lastUser(exec.lastReq); !strings.Contains(got, "task") {
		t.Errorf("executor should see raw task after blank plan, got %q", got)
	}
}
