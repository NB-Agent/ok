// Package agent implements the LLM agent run-loop.
package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NB-Agent/ok/internal/bus"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/diff"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/eventpipe"
	"github.com/NB-Agent/ok/internal/evidence"
	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

type Agent struct {
	prov                provider.Provider
	tools               tool.ToolRegistry
	session             *Session
	sessionPath         string // for mid-batch auto-save during tool chains
	maxSteps            int
	temperature         float64
	pricing             *provider.Pricing
	sink                event.Sink
	pipe                eventpipe.Sink // typed event pipeline (optional, may be nil)
	pipeMu              sync.RWMutex   // guards pipe (SetPipe vs concurrent reads)
	usage               *UsageTracker
	planMode            atomic.Bool
	gate                Gate
	gateMu              sync.RWMutex
	hooks               ToolHooks
	hooksMu             sync.RWMutex
	onPreEdit           func(diff.Change) // snapshot hook for checkpoint/rewind (see SetPreEditHook)
	asker               Asker
	askerMu             sync.RWMutex
	jobs                *jobs.Manager
	contextWindow       int
	compactRatio        float64
	recentKeep          int
	archiveDir          string
	toolCache           map[string]toolCacheEntry
	toolCacheVer        uint64
	toolCacheMu         sync.RWMutex
	compactedLastMu     sync.Mutex // guards consecutiveCompacts + compactStuck
	softCompactRatio    float64    // report growing context here (default 0.50)
	softCompactNoticed  bool       // one-shot gate for soft-ratio notice
	compactForceRatio   float64    // force-compaction high-water mark (default 0.90)
	compactStuck        bool       // latched when consecutive compactions exceed the limit
	consecutiveCompacts int        // consecutive turns where compaction fired
	keepPolicy          KeepPolicy // bitmask controlling which messages survive compaction
	lastPrefixShape     PrefixShape
	haveLastPrefixShape bool
	auditChain          *core.AuditChain
	msgbus              *bus.Bus
	onTurnCompleteMu    sync.RWMutex // dedicated lock — was borrowing toolCacheMu (audit #4)
	onTurnComplete      func(context.Context, string, string)
	evidenceLedger      *evidence.Ledger // per-turn ledger of host-observed tool receipts
}

// KeepPolicy is a bitmask controlling which messages are preserved beyond the
// recent tail during compaction.
type KeepPolicy int

const (
	KeepErrors     KeepPolicy = 1 << iota // preserve error: and blocked: tool results
	KeepUserMarked                        // preserve [[keep]], [keep], <keep>, <!-- keep --> marked user messages
)

// ToolMsg is published on the bus for tool execution events.
type ToolMsg struct {
	Name     string
	Args     string
	Result   string
	Err      string
	Duration time.Duration
}

// SetMsgBus wires an optional message bus for publishing execution events.
// nil disables publishing; all publish calls are nil-safe.
func (a *Agent) SetMsgBus(b *bus.Bus) {
	a.msgbus = b
}

// SetPreEditHook installs a pre-edit snapshot hook (see onPreEdit).
// The controller wires it to its per-session checkpoint store;
// nil disables capture. Fires only for non-ReadOnly tools that implement
// tool.Previewer (so bash, whose targets are unknowable, is never tracked).
func (a *Agent) SetPreEditHook(fn func(diff.Change)) { a.onPreEdit = fn }

// MemMsg is published on the bus for memory events.
type MemMsg struct {
	Path  string
	Scope string
	Note  string
}

// systemPrompt returns the system prompt from the session's first message.
func (a *Agent) systemPrompt() string {
	if msgs := a.session.Snapshot(); len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		return msgs[0].Content
	}
	return ""
}

// capturePrefixShape hashes the current cacheable prefix for cache diagnostics.
func (a *Agent) capturePrefixShape(schemas []provider.ToolSchema) PrefixShape {
	return CaptureShape(a.systemPrompt(), schemas, a.session.Gen())
}
