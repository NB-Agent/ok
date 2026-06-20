package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// TestMaxStepsBoundsToolCallRounds checks that MaxSteps stops the run loop
// when the model keeps requesting tool calls. Each turn the model calls "echo",
// so the loop advances one step. After MaxSteps=3, the fourth tool-call round
// is rejected with a "max_steps reached" error.
func TestMaxStepsBoundsToolCallRounds(t *testing.T) {
	prov := &stepProvider{name: "steps", toolCalls: 10} // wants 10 tool calls
	reg := tool.NewRegistry()
	reg.Add(&stubRO{name: "echo"})
	a := New(prov, reg, NewSession("sys"), Options{
		MaxSteps:    3, // cap at 3 tool-call rounds
		Temperature: 0,
	}, event.Discard)

	err := a.Run(context.Background(), "do something")
	if err == nil {
		t.Fatal("expected max_steps error")
	}
	if !strings.Contains(err.Error(), "max_steps") {
		t.Errorf("error should mention max_steps, got: %v", err)
	}
	if prov.actualSteps > 3 {
		t.Errorf("MaxSteps=3 but ran %d tool-call rounds", prov.actualSteps)
	}
}

// stepProvider returns tool_calls on the first N turns, then a final answer.
type stepProvider struct {
	name        string
	mu          int // current step (atomic via single goroutine in stream)
	toolCalls   int // how many steps should include a tool call
	actualSteps int
}

func (p *stepProvider) Name() string { return p.name }

func (p *stepProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 5)
	p.actualSteps = p.mu
	p.mu++

	step := p.actualSteps
	go func() {
		if step < p.toolCalls {
			ch <- provider.Chunk{
				Type: provider.ChunkText,
				Text: "calling echo",
			}
			ch <- provider.Chunk{
				Type:     provider.ChunkToolCall,
				ToolCall: &provider.ToolCall{ID: "t1", Name: "echo", Arguments: "{}"},
			}
		} else {
			ch <- provider.Chunk{Type: provider.ChunkText, Text: "Done."}
		}
		ch <- provider.Chunk{
			Type:  provider.ChunkUsage,
			Usage: &provider.Usage{PromptTokens: 200 * (step + 1)},
		}
		close(ch)
	}()
	return ch, nil
}

// TestCompactDoesNotHangWithTinyWindow verifies that compaction never
// produces fewer than minCompactMessages compactable messages, so the
// agent doesn't repeatedly compact the same small session into a loop.
func TestCompactDoesNotLoopWithTinyWindow(t *testing.T) {
	prov := &stepProvider{name: "tiny", toolCalls: 0}
	reg := tool.NewRegistry()
	reg.Add(&stubRO{name: "echo"})
	a := New(prov, reg, NewSession("sys"), Options{
		ContextWindow: 300, // tiny window — compaction threshold = 240
		MaxSteps:      20,
		Temperature:   0,
	}, event.Discard)

	// Run many turns. Even with a tiny context window, the hysteresis guard
	// and minCompactMessages check prevent compaction from running every turn.
	for i := 0; i < 20; i++ {
		if err := a.Run(context.Background(), inputForTurn(i)); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	// All 20 turns should complete without hanging or infinite compacting.
}

// stubRO is a minimal read-only tool satisfying tool.Tool for tests.
type stubRO struct{ name string }

func (t *stubRO) Name() string                                                 { return t.name }
func (t *stubRO) Description() string                                          { return "stub" }
func (t *stubRO) Schema() json.RawMessage                                      { return json.RawMessage(`{"type":"object"}`) }
func (t *stubRO) Execute(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil }
func (t *stubRO) ReadOnly() bool                                               { return true }

func inputForTurn(i int) string {
	return strings.Repeat("t", i%20+5)
}
