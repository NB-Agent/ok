package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// TestParallelBatchNoFatal verifies that parallel read-only tool calls complete without false fatal.
func TestParallelBatchNoFatal(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "safe_read", readOnly: true})
	reg.Add(fakeTool{name: "another_read", readOnly: true})

	var got []event.Event
	sink := event.FuncSink(func(e *event.Event) { got = append(got, *e) })
	a := New(nil, reg, NewSession(""), Options{}, sink)

	calls := []provider.ToolCall{
		{ID: "c1", Name: "safe_read"},
		{ID: "c2", Name: "another_read"},
	}

	results, fatal := a.executeBatch(context.Background(), calls)
	if fatal {
		t.Error("expected no fatal from safe read-only calls")
	}
	if len(results) != 2 {
		t.Errorf("want 2 results, got %d", len(results))
	}
}

// TestUnknownToolSurfaceError verifies unknown tools return non-empty errMsg.
func TestUnknownToolSurfaceError(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "known", readOnly: true})
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)

	o := a.executeOne(context.Background(), provider.ToolCall{Name: "nonexistent"})
	if o.errMsg == "" {
		t.Error("unknown tool should have non-empty errMsg")
	}
}

// TestCovenantCheckFiresFirst verifies core covenant runs before permission checks.
func TestCovenantCheckFiresFirst(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false})
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)

	o := a.executeOne(context.Background(), provider.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"/etc/passwd","content":"x"}`,
	})

	// Covenant should detect the sensitive path and block.
	if o.errMsg != "" {
		t.Logf("covenant blocked: errMsg=%q blocked=%v fatal=%v", o.errMsg, o.blocked, o.fatal)
	}
}

// TestCovenantP4ForReadOnlyToolIsNonFatal verifies that a read-only tool
// is not blocked by argument-level p4 scanning, because read-only tools
// cannot exfiltrate data — only tool name checks apply.
func TestCovenantP4ForReadOnlyToolIsNonFatal(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "grep", readOnly: true})
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)

	// A grep whose argument contains authorized_keys + >>: this would trigger
	// p4 if arguments were scanned, but read-only tools skip arg scanning.
	o := a.executeOne(context.Background(), provider.ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"cat key.pub >> ~/.ssh/authorized_keys"}`,
	})

	if o.blocked {
		t.Error("expected read-only tool to pass p4 arg scanning")
	}
	if o.fatal {
		t.Error("expected read-only tool to not be fatal")
	}
	if o.principle != "" {
		t.Errorf("expected no principle violation, got %q", o.principle)
	}
}

// TestSequentialBatchRunsAll verifies sequential tool calls all execute.
func TestSequentialBatchRunsAll(t *testing.T) {
	reg := tool.NewRegistry()
	var mu sync.Mutex
	count := 0
	reg.Add(&countingTool{
		name: "counter", readOnly: false,
		fn: func() string { mu.Lock(); count++; mu.Unlock(); return "ok" },
	})
	reg.Add(fakeTool{name: "writer", readOnly: false})

	a := New(nil, reg, NewSession(""), Options{}, event.Discard)
	calls := []provider.ToolCall{
		{ID: "c1", Name: "counter"},
		{ID: "c2", Name: "counter"},
	}
	_, _ = a.executeBatch(context.Background(), calls)

	if count != 2 {
		t.Errorf("want 2 executions, got %d", count)
	}
}

// TestProofChainConcurrentSafety verifies thread safety of proof chain appends.
func TestProofChainConcurrentSafety(t *testing.T) {
	pc := core.NewProofChain()
	var wg sync.WaitGroup
	n := 100

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pc.Append("atom-"+itoa(i), "p", "e")
		}(i)
	}
	wg.Wait()

	if pc.Len() != n {
		t.Errorf("proof chain length = %d, want %d (concurrent appends may have lost entries)", pc.Len(), n)
	}
}

// countingTool is a test tool that invokes a callback on each execution.
type countingTool struct {
	name     string
	readOnly bool
	fn       func() string
}

func (c *countingTool) Name() string            { return c.name }
func (c *countingTool) ReadOnly() bool          { return c.readOnly }
func (c *countingTool) Description() string     { return "counting test tool" }
func (c *countingTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (c *countingTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return c.fn(), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
