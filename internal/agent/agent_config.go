package agent

import (
	"context"

	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/eventpipe"
	"github.com/NB-Agent/ok/internal/evidence"
	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// SetHooks installs tool hooks. Nil disables.
func (a *Agent) SetHooks(h ToolHooks) {
	a.hooksMu.Lock()
	defer a.hooksMu.Unlock()
	a.hooks = h
}

// Hooks returns the currently installed tool hooks, or nil.
func (a *Agent) Hooks() ToolHooks { return a.getHooks() }

// getHooks returns the current hooks under lock.
func (a *Agent) getHooks() ToolHooks {
	a.hooksMu.RLock()
	defer a.hooksMu.RUnlock()
	return a.hooks
}

// Provider returns the LLM provider.

// non-ReadOnly tool the model calls and returns a "blocked" result instead of
// running it. The cache-friendly bits — system prompt, tools schema, message
// history — are left untouched, so the toggle costs nothing in cache hits.
func (a *Agent) SetPlanMode(v bool) { a.planMode.Store(v) }

// SetGate installs the per-call permission gate. Used by `ok chat` to swap the
// headless gate built in setup for an interactive one that prompts the user;
// nil disables gating. Safe to call before the run loop starts.
func (a *Agent) SetGate(g Gate) {
	a.gateMu.Lock()
	defer a.gateMu.Unlock()
	a.gate = g
}

// getGate returns the current gate under lock.
func (a *Agent) getGate() Gate {
	a.gateMu.RLock()
	defer a.gateMu.RUnlock()
	return a.gate
}

// SetAsker installs the asker the `ask` tool uses to question the user.
// Interactive frontends wire one in; headless runs leave it nil.
func (a *Agent) SetAsker(as Asker) {
	a.askerMu.Lock()
	defer a.askerMu.Unlock()
	a.asker = as
}

// getAsker returns the current asker under lock.
func (a *Agent) getAsker() Asker {
	a.askerMu.RLock()
	defer a.askerMu.RUnlock()
	return a.asker
}

// Session returns the agent's current conversation, useful for persistence
// hooks that need to read the message log between turns.
func (a *Agent) Session() *Session { return a.session }

// SetSessionPath sets the path for mid-batch auto-save during tool chains.
// Called by the controller after NewSession or Resume.
func (a *Agent) SetSessionPath(path string) { a.sessionPath = path }

// SetSession replaces the agent's conversation wholesale. Used by
// `ok chat --resume` to load a saved JSONL transcript before the first turn,
// so the model picks up exactly where it left off.
func (a *Agent) SetSession(s *Session) {
	a.toolCacheMu.Lock()
	defer a.toolCacheMu.Unlock()
	a.session = s
	a.usage.ResetSessionCache()
}

// SetOnTurnComplete registers a callback fired after each turn completes.
func (a *Agent) SetOnTurnComplete(fn func(context.Context, string, string)) {
	a.onTurnCompleteMu.Lock()
	a.onTurnComplete = fn
	a.onTurnCompleteMu.Unlock()
}

// LastUsage returns the most recent per-turn token telemetry the provider
// reported (nil if no turn has run yet). The TUI uses it to show a context
// gauge alongside the prompt; the actual cache decisions still live inside
// maybeCompact.
func (a *Agent) LastUsage() *provider.Usage {
	return a.usage.LastUsage()
}

// SetPipe installs a typed event pipeline sink. When set, the agent emits
// typed events directly to the pipeline instead of going through the old
// event.Sink. This is the migration path from old-style to typed events.
// Must be called before Run starts, or protected by pipeMu.
func (a *Agent) SetPipe(p eventpipe.Sink) {
	a.pipeMu.Lock()
	a.pipe = p
	a.pipeMu.Unlock()
}

// pipeSnapshot returns the current pipe under lock, safe for concurrent reads.
func (a *Agent) pipeSnapshot() eventpipe.Sink {
	a.pipeMu.RLock()
	defer a.pipeMu.RUnlock()
	return a.pipe
}

// SessionCache returns the cumulative cache hit/miss prompt tokens across every
// API call this session — the basis for the status line's aggregate hit-rate.
func (a *Agent) SessionCache() (hit, miss int) {
	return a.usage.SessionCache()
}

// ContextWindow returns the configured context-window size in tokens. 0
// means compaction is disabled for this agent.
func (a *Agent) ContextWindow() int { return a.contextWindow }

// AuditLog returns a copy of the audit chain entries, or nil if auditing is
// disabled.
func (a *Agent) AuditLog() []core.AuditRecord {
	if a.auditChain == nil {
		return nil
	}
	return a.auditChain.All()
}

// CompactNow runs one compaction pass immediately, regardless of the
// usage-ratio threshold maybeCompact normally honors. Used by the chat
// TUI's `/compact` command so the user can reset the prefix before it
// naturally fills up.
func (a *Agent) CompactNow(ctx context.Context) error { return a.compact(ctx) }

// Options configures an Agent.
type Options struct {
	MaxSteps    int
	Temperature float64
	Pricing     *provider.Pricing // optional, for per-turn cost display

	// Gate is the per-call permission gate. nil disables gating.
	Gate Gate

	// Hooks fires PreToolUse / PostToolUse shell hooks around tool calls. nil
	// disables hook firing.
	Hooks ToolHooks

	// Jobs is the session's background-job manager (nil disables background tools).
	Jobs *jobs.Manager

	// Context management. ContextWindow <= 0 disables compaction. CompactRatio
	// and RecentKeep fall back to defaults when unset.
	ContextWindow     int
	CompactRatio      float64
	CompactForceRatio float64
	RecentKeep        int
	ArchiveDir        string
	KeepPolicy        KeepPolicy

	// AuditChain is the tamper-evident audit log for tool execution.
	// nil disables auditing.
	AuditChain *core.AuditChain

	// OnTurnComplete, when set, is called after each complete turn (model returns
	// a final answer without tool calls). It receives the user's input and the
	// model's final text for episodic memory capture. The callee should not block.
	OnTurnComplete func(ctx context.Context, input, answer string)

	// SessionPath, when non-empty, enables mid-batch auto-save so crash recovery
	// loses at most 5 tool-call rounds per long tool chain.
	SessionPath string
}

// New constructs an Agent. MaxSteps <= 0 means no cap — the run loop continues
// until the model gives a final answer, the context is canceled, or the
// provider errors (compaction keeps the context bounded). A nil sink is replaced
// with event.Discard so the agent can always emit unconditionally.
func New(prov provider.Provider, tools tool.ToolRegistry, session *Session, opts Options, sink event.Sink) *Agent {
	if opts.CompactRatio <= 0 {
		opts.CompactRatio = defaultCompactRatio
	}
	if opts.RecentKeep <= 0 {
		opts.RecentKeep = defaultRecentKeep
	}
	if opts.Temperature < 0 {
		opts.Temperature = 0
	}
	if opts.Temperature > 2 {
		opts.Temperature = 2
	}
	if opts.CompactRatio > 0.90 {
		opts.CompactRatio = 0.90
	}
	if sink == nil {
		sink = event.Discard
	}
	// Wrap in Sync so parallel tool goroutines (executeBatch allReadOnly) can
	// emit ToolResult safely alongside the serial run loop.
	sink = event.Sync(sink)
	return &Agent{
		prov:           prov,
		tools:          tools,
		session:        session,
		sessionPath:    opts.SessionPath,
		maxSteps:       opts.MaxSteps,
		temperature:    opts.Temperature,
		pricing:        opts.Pricing,
		sink:           sink,
		usage:          NewUsageTracker(),
		gate:           opts.Gate,
		hooks:          opts.Hooks,
		jobs:           opts.Jobs,
		contextWindow:  opts.ContextWindow,
		compactRatio:   opts.CompactRatio,
		recentKeep:     opts.RecentKeep,
		archiveDir:     opts.ArchiveDir,
		auditChain:     opts.AuditChain,
		onTurnComplete: opts.OnTurnComplete,
		toolCache:      make(map[string]toolCacheEntry, 256),
		evidenceLedger: evidence.NewLedger(),
	}
}
