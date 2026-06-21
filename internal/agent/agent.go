// Package agent implements the LLM agent run-loop.
package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NB-Agent/ok/internal/bus"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/eventpipe"
	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

type Agent struct {
	prov             provider.Provider
	tools            tool.ToolRegistry
	session          *Session
	sessionPath      string // for mid-batch auto-save during tool chains
	maxSteps         int
	temperature      float64
	pricing          *provider.Pricing
	sink             event.Sink
	pipe             eventpipe.Sink // typed event pipeline (optional, may be nil)
	pipeMu           sync.RWMutex   // guards pipe (SetPipe vs concurrent reads)
	usage            *UsageTracker
	planMode         atomic.Bool
	gate             Gate
	gateMu           sync.RWMutex
	hooks            ToolHooks
	hooksMu          sync.RWMutex
	asker            Asker
	askerMu          sync.RWMutex
	jobs             *jobs.Manager
	contextWindow    int
	compactRatio     float64
	recentKeep       int
	archiveDir       string
	toolCache        map[string]toolCacheEntry
	toolCacheVer     uint64
	toolCacheMu      sync.RWMutex
	compactedLastMu  sync.Mutex // dedicated lock — was borrowing toolCacheMu (audit #4)
	compactedLast    bool
	streamAvgLatency time.Duration // sliding average of stream() call latency
	auditChain       *core.AuditChain
	msgbus           *bus.Bus
	onTurnCompleteMu sync.RWMutex // dedicated lock — was borrowing toolCacheMu (audit #4)
	onTurnComplete   func(context.Context, string, string)
	fileTrack        fileTracker // tracks file-read tool results for context compression
}

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

// MemMsg is published on the bus for memory events.
type MemMsg struct {
	Path  string
	Scope string
	Note  string
}
