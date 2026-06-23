// Package dstsetup wires compile/test regression checks into an Agent.
// Every write-file tool call is followed by a synchronous go build / go test
// check. Passes are recorded into a ProofChain for per-turn agent memory;
// failures roll back the file and surface a "rolled back: ..." error.
// No async LLM verification — the proof chain alone gives the agent
// structured memory of verified state across turns.
package dstsetup

import (
	"context"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/dstvalid"
	"github.com/NB-Agent/ok/internal/event"
)

// Init wires compile/test hooks into the agent and returns a DSTRunner.
// proofChain accumulates verification results for per-turn memory injection.
// workDir is the project root; compileCmd/testCmd are e.g. "go build ./...".
// If the agent is nil, returns nil.
func Init(a *agent.Agent, proofChain *core.ProofChain, workDir, compileCmd, testCmd string) *DSTRunner {
	if a == nil {
		return nil
	}

	hooks := dstvalid.NewDSTHooks(workDir)
	hooks.SetBuildCommands(compileCmd, testCmd)
	// Wire proof chain recording: every compile/test pass appends an entry.
	hooks.SetProofChain(proofChain)

	// Chain existing user hooks so DST and user hooks coexist.
	if existing := a.Hooks(); existing != nil {
		hooks.SetNext(existing)
	}
	a.SetHooks(hooks)

	return &DSTRunner{
		inner: a,
		hooks: hooks,
	}
}

// DSTRunner wraps an agent.Runner and injects compile/test hooks. It also
// serves as the DST facade for the Controller (toggle, status).
type DSTRunner struct {
	inner agent.Runner
	hooks *dstvalid.DSTHooks
	sink  event.Sink
}

// SetSink sets the event sink for DST status output.
func (d *DSTRunner) SetSink(s event.Sink) { d.sink = s }

// IsAvailable reports whether the guard was successfully initialised.
func (d *DSTRunner) IsAvailable() bool { return d != nil && d.hooks != nil }

// IsEnabled reports whether per-step compile/test checks are active.
// Safe to call on a nil *DSTRunner (returns false).
func (d *DSTRunner) IsEnabled() bool { return d != nil && d.hooks != nil && d.hooks.IsEnabled() }

// Enable turns on per-step compile/test hooks. Safe to call on a nil receiver.
func (d *DSTRunner) Enable() {
	if d != nil && d.hooks != nil {
		d.hooks.Enable()
	}
}

// Disable turns off compile/test checks. Safe to call on a nil receiver.
func (d *DSTRunner) Disable() {
	if d != nil && d.hooks != nil {
		d.hooks.Disable()
	}
}

// Run delegates to the inner runner (compile/test hooks fire in PostToolUse).
func (d *DSTRunner) Run(ctx context.Context, input string) error {
	return d.inner.Run(ctx, input)
}

// Hooks returns the DSTHooks instance, for wrapping with additional hook layers.
func (d *DSTRunner) Hooks() *dstvalid.DSTHooks { return d.hooks }

func (d *DSTRunner) notice(text string) {
	if d.sink != nil {
		d.sink.Emit(&event.Event{Kind: event.Notice, Text: "[DST] " + text})
	}
}
