// Package control is the transport-agnostic session driver. A Controller owns
// the agent run loop and session lifecycle, takes commands (Send/Cancel/Approve/
// SetPlanMode/Compact/NewSession) and emits everything that happens —
// reasoning, tool calls, approvals, turn completion — as a typed event stream to
// a single event.Sink.
//
// The point is one orchestration layer behind every frontend: a terminal TUI, a
// desktop webview, or an HTTP/SSE server each drive the Controller identically
// (issue commands, render events) and none of them re-implement turn lifecycle,
// cancellation, or approval. The Controller depends on no frontend.
//
// See also:
//
//	controller_turn.go     — turn lifecycle (runGuarded, Send, Submit, runTurn, Compose)
//	controller_approval.go — approval/ask/plan-mode (Approve, Ask, requestApproval)
//	controller_session.go  — session persistence (Snapshot, NewSession, Resume)
//	controller_mcp.go      — MCP server hot-add/remove
//	controller_memory.go   — memory quick-add/save
//	controller_query.go    — read-only accessors (History, Balance, Skills, DST, etc.)
//	input.go               — Compose, CustomCommand, RunSkill, MCPPrompt
//	slash.go               — slash-command completion and management notices
//	refs.go                — @-reference resolution
package control

// ─── File organization ───────────────────────────────────────────────────────────
//   controller.go           — Controller struct, New(), Options
//   controller_turn.go      — turn lifecycle (runGuarded, Send, runTurn)
//   controller_approval.go  — Approve, Answer, SetPlanMode, SetBypass
//   controller_session.go   — Snapshot, NewSession, Resume, Compact
//   controller_mcp.go       — AddMCPServer, RemoveMCPServer
//   controller_memory.go    — QuickAdd, Memorize, Memory
//   controller_query.go     — read accessors (History, Balance, Skills, Config, …)
//   input.go                — Compose, CustomCommand, RunSkill, MCPPrompt
//   approval.go             — ApprovalManager (ID gen, pipeline, permissive)
//   refs.go                 — @-reference resolution (files, MCP resources)
//   slash.go                — slash-command completion data

import (
	"context"
	"sync"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/bus"
	"github.com/NB-Agent/ok/internal/checkpoint"
	"github.com/NB-Agent/ok/internal/command"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/diff"
	"github.com/NB-Agent/ok/internal/dstsetup"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/evolution"
	"github.com/NB-Agent/ok/internal/hook"
	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/kernel"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/memory"
	"github.com/NB-Agent/ok/internal/permission"
	"github.com/NB-Agent/ok/internal/plugin"
	"github.com/NB-Agent/ok/internal/skill"
	"github.com/NB-Agent/ok/internal/tool"
)

// Controller drives one chat session. Construct with New; drive with the command
// methods; observe through the Sink passed in Options.
//
// Fields are grouped by concern:
//
//	[core]     runner, executor, sink, policy, label, systemPrompt, cleanup, baseCtx
//	[session]  sessionDir, sessionPath
//	[plugins]  host, reg, pluginCtx
//	[modules]  commands, skills, hooks, mem
//	[dst]      dst, workDir, proofChain
//	[billing]  balanceURL, balanceKey
//	[jobs]     jobs
//	[approval] approval, planMode
//	[turn]     turn, pendingMemory, bgWG, mu, cancel, running
type Controller struct {
	// --- core ---
	runner   agent.Runner
	executor agent.Executor
	sink     event.Sink
	policy   permission.Policy
	msgbus   *bus.Bus // pub-sub for decoupled component communication

	label        string
	systemPrompt string
	cleanup      func()

	// baseCtx is the parent context for every turn spawned by runGuarded.
	baseCtx    context.Context
	baseCancel context.CancelFunc

	// --- session ---
	sessionDir  string
	sessionPath string

	// --- plugins ---
	// host owns the running plugin connections.
	// reg is the live tool registry the executor reads each turn.
	// pluginCtx is the session-scoped context a hot-added stdio server binds to.
	host      *plugin.Host
	reg       *tool.Registry
	pluginCtx context.Context

	// --- modules ---
	commands []command.Command
	skills   []skill.Skill
	hooks    *hook.Runner // session hook runner; nil-safe (no hooks configured)
	mem      *memory.Set

	// --- dst ---
	dst        *dstsetup.DSTRunner
	workDir    string // project root; set from Options for DST compile/test checks
	proofChain *core.ProofChain
	ecpEngine  *evolution.Engine // evolution engine for ECP endpoints (optional)
	ecpSecret  string            // shared secret for ECP peer authentication
	auditChain *core.AuditChain

	// --- checkpoint ---
	cp     *checkpoint.Store // per-session checkpoint store (nil when disabled)
	cpRoot string            // workspace root for checkpoint restore guards

	// --- civilization primitives ---
	kernel *kernel.Kernel

	// --- billing ---
	balanceURL string
	balanceKey string

	// --- jobs ---
	jobs *jobs.Manager

	// --- approval ---
	// approval owns the interactive approval/ask state machine (ApprovalManager).
	// planMode gates tool execution at the controller level (it also sets the
	// executor's planMode for the read-only gate in agent).
	approval   *ApprovalManager
	onRemember func(rule string)
	planMode   bool

	// --- turn ---
	// pendingMemory holds memory notes added mid-session. bgWG tracks background
	// goroutines for /new and /compact. mu guards running/cancel/planMode/turn.
	pendingMemory []string
	bgWG          sync.WaitGroup
	mu            sync.RWMutex
	cancel        context.CancelFunc
	running       bool
	turn          int

	// cachedProofSummary avoids recomputing the proof-chain summary on every
	// turn when the chain hasn't changed.
	cachedProofSummary string
	proofSummaryLen    int
	proofDirty         bool

	// envDiagnosis is cached env+boot context injected into user message every
	// envDiagInterval turns, keeping the system-prompt prefix byte-stable.
	// Default 50: inject environment context every ~50 turns (~5-10 sessions worth).
	envDiagnosis    string
	envDiagInterval int

	// firstTurnInject carries memory, shared knowledge, and skill index that
	// ride the first user turn via Compose instead of the system prompt.
	firstTurnInject string
}

// Options carries the already-built pieces setup assembles. Lifecycle metadata
// lets the controller mint and rotate session files; Host/Commands are surfaced
// to frontends that resolve MCP prompts and slash commands.
type Options struct {
	Runner       agent.Runner
	Executor     agent.Executor
	Sink         event.Sink
	Policy       permission.Policy
	Label        string
	SystemPrompt string
	SessionDir   string
	SessionPath  string
	WorkDir      string // project root for DST compile/test checks
	Host         *plugin.Host
	Commands     []command.Command
	Skills       []skill.Skill
	Hooks        *hook.Runner
	Memory       *memory.Set
	Cleanup      func()
	// BalanceURL/BalanceKey wire the active provider's optional wallet-balance
	// endpoint and bearer key; empty when the provider declares no balance_url.
	BalanceURL string
	BalanceKey string
	// MsgBus is the session-scoped message bus for pub-sub communication.
	MsgBus *bus.Bus
	// Jobs is the session-scoped background-job manager (nil disables background jobs).
	Jobs *jobs.Manager
	// Registry is the executor's live tool set, and PluginCtx the session-scoped
	// context; both are needed for hot-adding MCP servers via AddMCPServer.
	Registry  *tool.Registry
	PluginCtx context.Context
	// DSTBrain is the compile/test guard (nil when DST is unavailable).
	DSTBrain *dstsetup.DSTRunner
	// ProofChain accumulates per-turn verification results (compile/test/file checks)
	// and exposes a ProofSummary() for injection into turn context.
	ProofChain *core.ProofChain
	// AuditChain is the tamper-evident audit log for tool execution.
	// nil disables auditing.
	AuditChain *core.AuditChain
	// OnRemember is called when the user picks "always allow" — persists the rule.
	OnRemember func(rule string)
	// EnvDiagnosis is a cached environment+boot diagnostic string injected into
	// the user message every 5 turns (not into the system prompt) so the
	// cache-stable prefix stays warm. Empty disables injection.
	EnvDiagnosis string

	// FirstTurnInject carries memory, shared knowledge, and skill index that
	// ride the first user turn via Compose instead of the system prompt, so
	// changes to memory/skills never break the cache-stable prefix.
	FirstTurnInject string
	// Kernel carries civilization primitives (identity, recall, trust, learn).
	Kernel *kernel.Kernel
	// EvolEngine is the self-evolution engine for ECP federation endpoints (optional).
	EvolEngine *evolution.Engine
	// EvolSecret is the shared HMAC secret for ECP peer authentication.
	EvolSecret string
	// CheckpointRoot is the workspace root that checkpoint restores are confined
	// to ("" = no checkpoints). The controller creates a per-session checkpoint
	// store under <SessionPath>.ckpt when non-empty.
	CheckpointRoot string
}

// New builds a Controller. A nil Sink is replaced with event.Discard.
func New(opts Options) *Controller {
	sink := opts.Sink
	if sink == nil {
		sink = event.Discard
	}
	pluginCtx := opts.PluginCtx
	if pluginCtx == nil {
		pluginCtx = context.Background()
	}
	ctrl := &Controller{
		runner:          opts.Runner,
		executor:        opts.Executor,
		sink:            sink,
		policy:          opts.Policy,
		msgbus:          opts.MsgBus,
		label:           opts.Label,
		systemPrompt:    opts.SystemPrompt,
		sessionDir:      opts.SessionDir,
		sessionPath:     opts.SessionPath,
		workDir:         opts.WorkDir,
		host:            opts.Host,
		commands:        opts.Commands,
		skills:          opts.Skills,
		hooks:           opts.Hooks,
		mem:             opts.Memory,
		cleanup:         opts.Cleanup,
		kernel:          opts.Kernel,
		ecpEngine:       opts.EvolEngine,
		ecpSecret:       opts.EvolSecret,
		balanceURL:      opts.BalanceURL,
		balanceKey:      opts.BalanceKey,
		jobs:            opts.Jobs,
		reg:             opts.Registry,
		pluginCtx:       pluginCtx,
		dst:             opts.DSTBrain,
		proofChain:      opts.ProofChain,
		auditChain:      opts.AuditChain,
		approval:        NewApprovalManager(sink),
		onRemember:      opts.OnRemember,
		envDiagnosis:    opts.EnvDiagnosis,
		envDiagInterval: 50,
		firstTurnInject: opts.FirstTurnInject,
	}
	ctrl.baseCtx, ctrl.baseCancel = context.WithCancel(context.Background())
	// Wire the message bus into the executor for tool/turn pub-sub events.
	if ctrl.msgbus != nil && ctrl.executor != nil {
		ctrl.executor.SetMsgBus(ctrl.msgbus)
	}
	// Wire checkpoint pre-edit hook into the executor so every file write is
	// snapshotted before it touches disk, enabling /rewind.
	ctrl.cpRoot = opts.CheckpointRoot
	if ctrl.cpRoot != "" && ctrl.executor != nil {
		ctrl.executor.SetPreEditHook(func(ch diff.Change) {
			if ctrl.cp != nil {
				ctrl.cp.Snapshot(ch)
			}
		})
	}
	return ctrl
}

// --- lifecycle ---

// Label returns the human-readable model label, e.g. "deepseek-flash".
func (c *Controller) Label() string { return c.label }

// ECPEngine returns the evolution engine for ECP federation (may be nil).
func (c *Controller) ECPEngine() *evolution.Engine { return c.ecpEngine }

// ECPSharedSecret returns the HMAC shared secret for ECP peer authentication.
func (c *Controller) ECPSharedSecret() string { return c.ecpSecret }

// Running reports whether a turn is in flight.
func (c *Controller) Running() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

// notice emits a Notice event.
func (c *Controller) notice(text string) {
	c.sink.Emit(&event.Event{Kind: event.Notice, Text: text})
}

// Close stops plugin subprocesses and releases resources.
func (c *Controller) Close() {
	c.baseCancel()
	c.Cancel() // cancel any in-flight turn before tearing down
	// Auto-save before exit so at most one turn of work is lost.
	if c.executor != nil {
		if p := c.SessionPath(); p != "" {
			if err := c.executor.Session().Save(p); err != nil {
				log.Warn("close: save session", "path", p, "err", err)
			}
			// Also persist proof and audit chains alongside the session.
			if c.proofChain != nil && c.proofChain.Len() > 0 {
				if err := c.proofChain.Save(p + ".proof.json"); err != nil {
					log.Warn("close: save proof chain", "path", p+".proof.json", "err", err)
				}
			}
			if c.auditChain != nil && c.auditChain.Len() > 0 {
				if err := c.auditChain.Save(p + ".audit.json"); err != nil {
					log.Warn("close: save audit chain", "path", p+".audit.json", "err", err)
				}
			}
		}
	}
	c.bgWG.Wait() // drain fire-and-forget goroutines (/new, /compact)
	if c.jobs != nil {
		c.jobs.Close() // cancel any still-running background jobs
	}
	if c.cleanup != nil {
		c.cleanup()
	}
}

// SetBaseContext replaces the parent context used by runGuarded to spawn turn
// contexts. Set it before the first turn so cancellation propagates from the
// caller (e.g. server shutdown) into running turns. Safe to call concurrently
// with running turns — only the next spawned turn picks up the new context.
func (c *Controller) SetBaseContext(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baseCtx = ctx
}

// SetBypass turns YOLO/bypass mode on or off for the session: while on, every
// approval prompt is auto-allowed (writers and bash run without asking). Deny
// rules still block. Runtime-only — never written to config.
func (c *Controller) SetBypass(on bool) {
	c.approval.SetBypass(on)
}

// Bypass reports whether YOLO/bypass mode is on, for the status-bar indicator.
func (c *Controller) Bypass() bool {
	return c.approval.Bypass()
}
