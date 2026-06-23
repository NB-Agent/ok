package dstsetup

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/tool"
)

// fakeRunner records that Run was called.
type fakeRunner struct {
	called bool
}

func (f *fakeRunner) Run(ctx context.Context, input string) error {
	f.called = true
	return nil
}

func TestInitNilAgent(t *testing.T) {
	if d := Init(nil, nil, ".", "go build", "go test"); d != nil {
		t.Fatal("Init(nil) must return nil")
	}
}

func TestInitSetsHooks(t *testing.T) {
	a := agent.New(nil, tool.NewRegistry(), agent.NewSession("test"), agent.Options{}, event.Discard)
	pc := core.NewProofChain()

	d := Init(a, pc, ".", "go build", "")

	if d == nil {
		t.Fatal("Init must return non-nil DSTRunner")
	}
	if a.Hooks() == nil {
		t.Fatal("Init must install hooks on the agent")
	}
}

func TestInitChainsExistingHooks(t *testing.T) {
	a := agent.New(nil, tool.NewRegistry(), agent.NewSession("test"), agent.Options{}, event.Discard)

	// First install a dummy hook.
	existing := &stubHooks{preCalled: false, postCalled: false}
	a.SetHooks(existing)

	pc := core.NewProofChain()
	_ = Init(a, pc, ".", "go build", "")

	// Fire PreToolUse — must chain to the existing hook.
	hooks := a.Hooks()
	_, _ = hooks.PreToolUse(context.Background(), "write_file", json.RawMessage(`{"path":"/tmp/x"}`))
	if !existing.preCalled {
		t.Error("Init must chain PreToolUse to existing hooks")
	}

	// Fire PostToolUse — must chain.
	hooks.PostToolUse(context.Background(), "write_file", json.RawMessage(`{"path":"/tmp/x"}`), "ok")
	if !existing.postCalled {
		t.Error("Init must chain PostToolUse to existing hooks")
	}

	// ConsumeRollback must also work (returns false from stub).
	_, _, rb := hooks.ConsumeRollback()
	if rb {
		t.Error("stub ConsumeRollback should return false")
	}
}

func TestDSTRunnerAvailability(t *testing.T) {
	a := agent.New(nil, tool.NewRegistry(), agent.NewSession("test"), agent.Options{}, event.Discard)
	d := Init(a, core.NewProofChain(), ".", "go build", "")

	if !d.IsAvailable() {
		t.Error("fresh DSTRunner must be available")
	}
}

func TestDSTRunnerEnableDisable(t *testing.T) {
	a := agent.New(nil, tool.NewRegistry(), agent.NewSession("test"), agent.Options{}, event.Discard)
	d := Init(a, core.NewProofChain(), ".", "go build", "")

	if !d.IsEnabled() {
		t.Error("DSTRunner must start enabled (DST checks active by default)")
	}

	d.Disable()
	if d.IsEnabled() {
		t.Error("DSTRunner must be disabled after Disable()")
	}

	d.Enable()
	if !d.IsEnabled() {
		t.Error("DSTRunner must be enabled after Enable()")
	}
}

func TestDSTRunnerEnableDisableNoopOnNil(t *testing.T) {
	d := &DSTRunner{} // no hooks
	d.Enable()
	if d.IsEnabled() {
		t.Error("Enable() with nil hooks must be no-op")
	}
	d.Disable()
	if d.IsEnabled() {
		t.Error("Disable with nil hooks must be no-op")
	}
}

func TestDSTRunnerRunDelegates(t *testing.T) {
	fr := &fakeRunner{}
	d := &DSTRunner{inner: fr}

	if err := d.Run(context.Background(), "hello"); err != nil {
		t.Fatal("Run must succeed:", err)
	}
	if !fr.called {
		t.Error("Run must delegate to inner runner")
	}
}

func TestDSTRunnerSetSink(t *testing.T) {
	d := &DSTRunner{}
	var emitted bool
	d.SetSink(event.FuncSink(func(e *event.Event) { emitted = true }))
	d.notice("test")
	if !emitted {
		t.Error("SetSink must wire the notice emission")
	}
}

func TestIsAvailableFalseOnNil(t *testing.T) {
	var d *DSTRunner
	if d.IsAvailable() {
		t.Error("nil DSTRunner must not be available")
	}
}

// stubHooks implements agent.ToolHooks for testing chaining.
type stubHooks struct {
	preCalled  bool
	postCalled bool
}

func (s *stubHooks) PreToolUse(ctx context.Context, name string, args json.RawMessage) (bool, string) {
	s.preCalled = true
	return false, ""
}

func (s *stubHooks) PostToolUse(ctx context.Context, name string, args json.RawMessage, result string) {
	s.postCalled = true
}

func (s *stubHooks) ConsumeRollback() (string, string, bool) { return "", "", false }

// Ensure stubHooks satisfies the interface at compile time.
var _ agent.ToolHooks = (*stubHooks)(nil)
