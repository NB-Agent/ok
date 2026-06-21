package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// DefaultTaskSystemPrompt steers a sub-agent toward focused, terse delivery.
// Sub-agents have the same capabilities as the main agent: they can read,
// write, run commands, and spawn further sub-agents. The parent's task tree
// keeps nesting visible.
const DefaultTaskSystemPrompt = `You are a focused sub-agent. You have full access to all tools including bash, write_file, edit_file, and task(). Complete your task efficiently and return a concise result.`

// TaskTool spawns a sub-agent in its own session for a focused sub-task. The
// sub-agent runs with a filtered tool whitelist and the same step budget shape
// as the parent (see Execute); its tool calls are forwarded to the parent's
// event stream nested under this call, while only its final assistant message is
// returned to the parent model. Use cases: keep noisy tool sequences (multi-file
// exploration, repeated grep / read_file) out of the parent's context budget, or
// parallel research across independent areas (the parallel-dispatch path picks
// these up only when readOnly, which task is not).

// Snapshotter captures and rolls back file state. Each call to Execute should
// get a fresh instance via the factory to keep layers isolated.
type Snapshotter interface {
	CaptureDir(dir string) error
	Rollback() error
	Clear()
}

// ProofRecorder records verification results into the proof chain.
type ProofRecorder interface {
	AppendWithPath(atomID, proposition, evidence, parentID, path string)
}

type TaskTool struct {
	prov            provider.Provider
	pricing         *provider.Pricing
	parentReg       *tool.Registry
	maxSteps        int
	contextWindow   int
	temperature     float64
	archiveDir      string
	sysPrompt       string
	gate            Gate
	workDir         string
	newSnap         func() Snapshotter
	proofRecorder   ProofRecorder
	hooks           ToolHooks
	jm              *jobs.Manager
	subPromptSuffix string
	sem             chan struct{}
	lostSlots       atomic.Int32
	seq             int64
}

// NewTaskTool wires a task tool to the parent agent's environment so its
// sub-agents can use the same provider and tools. sysPrompt is the system
// prompt every sub-agent starts with; pass "" for DefaultTaskSystemPrompt. gate
// is the permission gate sub-agents inherit — pass the headless variant so
// deny rules still bite while autonomous sub-agents are never blocked on an
// interactive prompt (there is no UI to answer one).
func newTaskTool(prov provider.Provider, pricing *provider.Pricing, parentReg *tool.Registry,
	maxSteps, contextWindow int, temperature float64, archiveDir, sysPrompt string, gate Gate, workDir string,
	newSnap func() Snapshotter, proofRecorder ProofRecorder, maxConcurrent int,
	hooks ToolHooks, jm *jobs.Manager, subPromptSuffix string) *TaskTool {
	if sysPrompt == "" {
		sysPrompt = DefaultTaskSystemPrompt
	}
	var sem chan struct{}
	if maxConcurrent > 0 {
		sem = make(chan struct{}, maxConcurrent)
	}
	return &TaskTool{
		prov: prov, pricing: pricing, parentReg: parentReg,
		maxSteps: maxSteps, contextWindow: contextWindow, temperature: temperature,
		archiveDir: archiveDir, sysPrompt: sysPrompt, gate: gate, workDir: workDir,
		newSnap: newSnap, proofRecorder: proofRecorder,
		hooks: hooks, jm: jm, subPromptSuffix: subPromptSuffix, sem: sem,
	}
}

// NewTaskTool is the backward-compatible 13-arg constructor (hooks/jm/lang default to nil).
func NewTaskTool(prov provider.Provider, pricing *provider.Pricing, parentReg *tool.Registry,
	maxSteps, contextWindow int, temperature float64, archiveDir, sysPrompt string, gate Gate, workDir string,
	newSnap func() Snapshotter, proofRecorder ProofRecorder, maxConcurrent int) *TaskTool {
	return newTaskTool(prov, pricing, parentReg, maxSteps, contextWindow, temperature,
		archiveDir, sysPrompt, gate, workDir, newSnap, proofRecorder, maxConcurrent,
		nil, nil, "")
}

// NewTaskToolFull creates a TaskTool with sub-agent hooks, jobs manager, and prompt suffix.
func NewTaskToolFull(prov provider.Provider, pricing *provider.Pricing, parentReg *tool.Registry,
	maxSteps, contextWindow int, temperature float64, archiveDir, sysPrompt string, gate Gate, workDir string,
	newSnap func() Snapshotter, proofRecorder ProofRecorder, maxConcurrent int,
	hooks ToolHooks, jm *jobs.Manager, subPromptSuffix string) *TaskTool {
	return newTaskTool(prov, pricing, parentReg, maxSteps, contextWindow, temperature,
		archiveDir, sysPrompt, gate, workDir, newSnap, proofRecorder, maxConcurrent,
		hooks, jm, subPromptSuffix)
}

func (t *TaskTool) Name() string { return "task" }

func (t *TaskTool) Description() string {
	if t.sem == nil {
		return "Spawn a sub-agent. Full access to all tools including bash/write/task(). Can decompose recursively."
	}
	return fmt.Sprintf("Spawn a sub-agent. %d/%d slots active. Full access — can write and decompose recursively.",
		t.Active(), cap(t.sem))
}

func (t *TaskTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "prompt":{"type":"string","description":"What the sub-agent should accomplish."},
  "description":{"type":"string","description":"Short label (3-7 words)."},
  "path":{"type":"string","description":"Tree path like 'X→A→A1'. Shows where this task fits. Filled by parent agent."},
  "tools":{"type":"array","items":{"type":"string"},"description":"Optional tool whitelist."},
  "max_steps":{"type":"integer","description":"Cap on tool-call rounds.","minimum":1},
  "run_in_background":{"type":"boolean","description":"Run asynchronously across turns."},
  "allow_writes":{"type":"boolean","description":"Allow sub-agent to write/edit files and run bash. Its context is fully isolated — only the final answer returns to the parent. Failed tasks auto-rollback."}
},
"required":["prompt"]
}`)
}

// ReadOnly returns true. The sub-agent spawned by task is confined to
// read-only tools by buildSubReg (which excludes bash, write_file, etc.),
// so plan mode can safely allow task calls. This also lets task parallelise
// with other read-only tools. An explicit p.Tools whitelist can still grant
// writers to the sub-agent, but that is opt-in.
func (t *TaskTool) ReadOnly() bool { return true }

func (t *TaskTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Prompt          string   `json:"prompt"`
		Description     string   `json:"description"`
		Path            string   `json:"path"`
		Tools           []string `json:"tools"`
		MaxSteps        int      `json:"max_steps"`
		RunInBackground bool     `json:"run_in_background"`
		AllowWrites     bool     `json:"allow_writes"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}

	// Concurrency gate: acquire a slot.
	// Background: slot held for the full duration (runs across turns).
	// Foreground: slot is RELEASED during runSub (parent goroutine is idle waiting
	// for the child). This prevents deadlock when multiple foreground tasks are
	// waiting — each releases its slot while blocked so others can proceed.
	// Slot is re-acquired after runSub returns so recordResult runs under the gate.
	slot := t.semQueue(p.RunInBackground)
	if p.RunInBackground && slot > 0 {
		return fmt.Sprintf("queued (position %d/%d active). Wait for completions before spawning more.", slot, t.Active()), nil
	}
	if !p.RunInBackground && slot > 0 {
		return "", fmt.Errorf("task slots full (%d active, %d capacity) — reduce parallelism or wait", t.Active(), cap(t.sem))
	}
	if slot == 0 && !p.RunInBackground {
		// Release slot while blocked on child so children can use it.
		<-t.sem
		defer func() {
			// Context-aware slot re-acquisition: use remaining context time
			// (min 5s, max 30s) so we don't attempt to re-acquire after
			// the parent turn has already expired. If re-acquire succeeds,
			// immediately release the slot — it was only needed to protect
			// recordResult; after Execute returns the slot must be freed
			// so subsequent tasks can use it.
			var slotTimeout = 30 * time.Second
			if dl, ok := ctx.Deadline(); ok {
				remaining := time.Until(dl)
				if remaining <= 0 {
					t.lostSlots.Add(1)
					return
				}
				if remaining < slotTimeout {
					slotTimeout = remaining
				}
				if slotTimeout < 5*time.Second {
					slotTimeout = 5 * time.Second
				}
			}
			select {
			case t.sem <- struct{}{}:
				// Re-acquired — release it now that Execute is returning.
				<-t.sem
			case <-time.After(slotTimeout):
				t.lostSlots.Add(1)
			case <-ctx.Done():
				t.lostSlots.Add(1)
			}
		}()
	}
	// Background path: slot held for full duration (deferred release below).
	if slot == 0 && p.RunInBackground {
		defer func() { <-t.sem }()
	}

	desc := p.Description
	if desc == "" {
		desc = "task"
	}

	// Path from JSON takes priority; fall back to prompt parsing. Default to desc.
	path := p.Path
	if path == "" {
		path = extractPathFromPrompt(p.Prompt)
	}
	if path == "" {
		path = desc
	}
	// Add unique counter when no explicit tree path from parent Agent.
	if p.Path == "" && !strings.Contains(p.Prompt, "[Path]") && !strings.Contains(p.Prompt, "Path:") {
		path = fmt.Sprintf("%s-%d", path, atomic.AddInt64(&t.seq, 1))
	}

	maxSteps := p.MaxSteps
	if maxSteps <= 0 {
		if t.maxSteps > 0 {
			maxSteps = t.maxSteps / 2
			if maxSteps < 3 {
				maxSteps = 3
			}
		}
	}
	// Cap explicit max_steps to prevent the model from bypassing limits.
	// Default sub-agents should stay bounded; the parent's maxSteps (if set)
	// is the absolute ceiling.
	if t.maxSteps > 0 && (maxSteps <= 0 || maxSteps > t.maxSteps) {
		maxSteps = t.maxSteps
	}

	subReg := t.buildSubReg(p.Tools, p.AllowWrites)

	if p.RunInBackground {
		jm, ok := jobs.FromContext(ctx)
		if !ok {
			return "", fmt.Errorf("background execution is not available in this context")
		}
		parentID, parent, _, _ := CallContext(ctx)
		nested := subSinkFor(parentID, parent, true) // background: don't race main agent text
		rec := t.proofRecorder
		newSnap := t.newSnap
		workDir := t.workDir
		job := jm.Start("task", desc, func(jobCtx context.Context, _ io.Writer) (string, error) {
			// Snapshot captured at execution time, not enqueue time, so
			// unrelated writes from the same batch are not reverted if
			// this task fails and rolls back.
			var snap Snapshotter
			if newSnap != nil && workDir != "" {
				snap = newSnap()
				if err := snap.CaptureDir(workDir); err != nil {
					snap.Clear()
					snap = nil
				}
			}
			defer func() {
				if r := recover(); r != nil {
					if snap != nil {
						_ = snap.Rollback()
					}
					panic(r)
				}
				if snap != nil {
					snap.Clear()
				}
			}()
			r, e := t.runSub(jobCtx, p.Prompt, subReg, nested, maxSteps)
			return recordResult(r, e, snap, rec, desc, p.Prompt, path)
		})
		return fmt.Sprintf("Started background task %q (%s).", job.ID, desc), nil
	}

	// Foreground: snapshot captured immediately before runSub — synchronous,
	// so no risk of unrelated writes from the same batch contaminating the
	// baseline.
	var snap Snapshotter
	if t.newSnap != nil && t.workDir != "" {
		snap = t.newSnap()
		if err := snap.CaptureDir(t.workDir); err != nil {
			snap.Clear()
			snap = nil
		}
	}

	// Foreground. Guard snapshot with panic recovery: on panic, rollback before
	// re-panicking so partial writes never survive a crash. On normal return the
	// defer clears the snapshot (recordResult handles rollback for error/failure).
	defer func() {
		if r := recover(); r != nil {
			if snap != nil {
				_ = snap.Rollback() // best-effort rollback on panic
			}
			snap = nil // prevent Clear from running; we already rolled back
			panic(r)   // re-panic — let the framework's recover in runGuarded handle it
		}
		if snap != nil {
			snap.Clear()
		}
	}()
	r, e := t.runSub(ctx, p.Prompt, subReg, subSink(ctx), maxSteps)
	return recordResult(r, e, snap, t.proofRecorder, desc, p.Prompt, path)
}

// recordResult handles snapshot/rollback + proof recording for a completed sub-agent.
func recordResult(result string, err error, snap Snapshotter, rec ProofRecorder, desc, prompt, path string) (string, error) {
	if err != nil {
		// snapshot is cleared by the caller's defer; rollback on failure
		// so any partial writes are reverted (best-effort).
		if snap != nil {
			if rbErr := snap.Rollback(); rbErr != nil {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
			}
		}
		return "", err
	}
	if isTaskFailed(result) {
		if snap != nil {
			if rbErr := snap.Rollback(); rbErr != nil {
				result = fmt.Sprintf("rollback error: %v\n%s", rbErr, result)
			}
		}
		if rec != nil {
			rec.AppendWithPath(desc, prompt, "FAIL: "+result, "", path)
		}
		return "NO (rolled back)\n" + result, nil
	}
	// Success path: defer already handles snap.Clear()
	if rec != nil {
		rec.AppendWithPath(desc, prompt, "OK", "", path)
	}
	return result, nil
}

// buildSubReg returns the sub-agent's tool set: the named whitelist, or every
// parent tool. By default, execution tools (the right side of the center line —
// writes, commands, desktop control) are excluded so task sub-agents stay on the
// left (verification/analysis) side. When allowWrites is true and no explicit
// whitelist is given, writes/bash are included — the sub-agent implements changes
// in full isolation, with auto-rollback on failure.
// task() itself is excluded — sub-agents are flat leaves; the main agent owns
// decomposition.
func (t *TaskTool) buildSubReg(names []string, allowWrites bool) *tool.Registry {
	// Static exclude lists — avoid re-allocating on every task() call.
	var exclude []string
	if len(names) == 0 {
		if allowWrites {
			// Full-access sub-agent: include writes/bash but block recursion.
			exclude = subExcludeFullAccess
		} else {
			// Default: read-only sub-agent
			exclude = defaultSubExclude
		}
	} else {
		// Explicit whitelist: caller chose these tools. Only block recursion.
		exclude = subExcludeWhitelist
	}
	return FilterRegistry(t.parentReg, names, exclude...)
}

// Pre-allocated exclude lists for buildSubReg to avoid GC churn on every task().
var (
	defaultSubExclude    = []string{"review", "security_review", "run_skill", "install_skill", "remember", "auto-heal", "deploy", "desktop", "computer-use", "tool-groups", "make-tool", "kill_shell"}
	subExcludeWhitelist  = []string{"task", "run_skill", "install_skill", "review", "security_review"}
	subExcludeFullAccess = []string{"task", "run_skill", "install_skill", "review", "security_review"}
)

// FilterRegistry builds a sub-registry from parent: the named whitelist (empty =
// every parent tool), minus any excluded names. Used to scope what a spawned
// sub-agent — a `task` sub-agent or a subagent skill — may call.
func FilterRegistry(parent *tool.Registry, names []string, exclude ...string) *tool.Registry {
	sub := tool.NewRegistry()
	src := names
	if len(src) == 0 {
		src = parent.Names()
	}
	ex := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		ex[e] = true
	}
	for _, name := range src {
		if ex[name] {
			continue
		}
		if tl, ok := parent.GetAny(name); ok {
			sub.Add(tl)
		}
	}
	return sub
}

// runSub builds a sub-agent over subReg, runs prompt to completion emitting to
// sink, and returns its final assistant answer. Shared by the foreground and
// background paths.
func (t *TaskTool) runSub(ctx context.Context, prompt string, subReg *tool.Registry, sink event.Sink, maxSteps int) (string, error) {
	sysPrompt := t.sysPrompt
	if t.subPromptSuffix != "" {
		sysPrompt += "\n\n" + t.subPromptSuffix
	}
	return RunSubAgent(ctx, t.prov, subReg, sysPrompt, prompt, Options{
		MaxSteps: maxSteps, Temperature: t.temperature, Pricing: t.pricing,
		Gate: t.gate, Hooks: t.hooks, Jobs: t.jm,
		ContextWindow: t.contextWindow, ArchiveDir: t.archiveDir,
	}, sink)
}

// RunSubAgent runs prompt to completion in a fresh sub-agent session over reg,
// emitting tool activity to sink, and returns the sub-agent's final assistant
// answer. It is the shared core behind the `task` tool and subagent skills: a
// caller supplies the system prompt (the task persona or the skill body), the
// tool registry (already filtered), and the run Options (model budget, gate).
func RunSubAgent(ctx context.Context, prov provider.Provider, reg *tool.Registry, sysPrompt, prompt string, opts Options, sink event.Sink) (string, error) {
	sess := NewSession(sysPrompt)
	sub := New(prov, reg, sess, opts, sink)
	if err := sub.Run(ctx, prompt); err != nil {
		return "", fmt.Errorf("sub-agent: %w", err)
	}
	// Walk the session backwards for the last assistant message with content —
	// that's the sub-agent's final answer. Intermediate assistant messages with
	// tool_calls but no text don't count.
	msgs := sess.Snapshot()
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == provider.RoleAssistant && strings.TrimSpace(m.Content) != "" {
			return m.Content, nil
		}
	}
	return "", fmt.Errorf("sub-agent finished without producing a final answer")
}

// NestedSink returns a sink that forwards a sub-agent's tool activity to the
// parent stream, nested under the tool call carried by ctx, so a frontend shows
// it beneath that call (the same nesting `task` uses). Falls back to the given
// sink when ctx carries no call context. Used by subagent skills.
func NestedSink(ctx context.Context, fallback event.Sink) event.Sink {
	parentID, parent, _, ok := CallContext(ctx)
	if !ok || parent == nil {
		return fallback
	}
	return subSinkFor(parentID, parent, false) // foreground: forward live
}

// subSink forwards a sub-agent's tool dispatch/result events to the parent's
// event stream, tagged with the parent task call's ID so a frontend nests them
// under it. The sub-agent's own turn/usage/text/reasoning events are dropped —
// only its tool activity (the part worth seeing live) and its final answer
// (returned by Execute) reach the parent. The forwarded call IDs are namespaced
// with the parent ID so a sub-agent call can never collide with a parent call in
// the frontend's dispatch→result matching. Falls back to Discard when there's no
// parent stream (the headless run loop, or a direct Execute in tests).
func subSink(ctx context.Context) event.Sink {
	parentID, parent, _, ok := CallContext(ctx)
	if !ok || parent == nil {
		return event.Discard
	}
	return subSinkFor(parentID, parent, false) // foreground: forward live
}

// subSinkFor builds the nesting sink from an already-captured parent ID + stream,
// for the background path where the job runs under a context that no longer
// carries the call context. Falls back to Discard when there's no parent stream.
func subSinkFor(parentID string, parent event.Sink, bg bool) event.Sink {
	if parent == nil {
		return event.Discard
	}
	return event.FuncSink(func(e *event.Event) {
		switch e.Kind {
		case event.ToolDispatch, event.ToolResult:
			e.Tool.ParentID = parentID
			e.Tool.ID = parentID + "/" + e.Tool.ID
			if !bg {
				parent.Emit(e) // foreground: stream live
			}
			// Background: drained atomically via Jobs.DrainCompletedNote(),
			// preventing stray tool-result events from appearing after
			// the main agent's turn text already rendered.
		default:
		}
	})
}

// isTaskFailed reports whether a sub-agent's final answer indicates failure.
// Only explicit NO/❌ markers signal failure; everything else (YES, ✅, or
// any other answer like "The verification passed") is treated as success.
func isTaskFailed(result string) bool {
	trimmed := strings.TrimSpace(result)
	return trimmed == "NO" ||
		strings.HasPrefix(trimmed, "NO ") ||
		strings.HasPrefix(trimmed, "NO\n") ||
		strings.HasPrefix(trimmed, "NO\r") ||
		strings.HasPrefix(trimmed, "❌")
}

// Active reports the number of currently-running sub-agents.
func (t *TaskTool) Active() int {
	if t.sem == nil {
		return 0
	}
	return len(t.sem)
}

// semQueue tries to acquire a concurrency slot. For foreground tasks (blocking
// = true) it blocks up to 30 s; for background tasks it is non-blocking.
// Returns the queue position (0 = acquired, >0 = position, -1 = no semaphore).
// Lost slots (from defer re-acquire timeouts) are recovered transparently.
func (t *TaskTool) semQueue(background bool) int {
	if t.sem == nil {
		return -1 // no semaphore → unlimited
	}
	// Recover lost slots with a CAS loop: a lost slot means one fewer item
	// is in the channel than expected (it timed out in a defer re-acquire).
	// Two goroutines racing through here must not both claim the same lost
	// slot — that would grant a phantom free slot and exceed capacity.
	// When CAS succeeds but the channel is already full (default branch),
	// the slot wasn't actually lost — restore lostSlots and exit the loop
	// so normal acquisition can proceed.
compLoop:
	for {
		v := t.lostSlots.Load()
		if v <= 0 {
			break
		}
		if t.lostSlots.CompareAndSwap(v, v-1) {
			select {
			case t.sem <- struct{}{}:
				return 0 // compensated and acquired
			default:
				// Channel full — no compensation needed; restore lostSlots.
				t.lostSlots.Add(1)
				break compLoop
			}
		}
	}
	if background {
		select {
		case t.sem <- struct{}{}:
			return 0
		default:
			return len(t.sem) + 1 // position in queue
		}
	}
	select {
	case t.sem <- struct{}{}:
		return 0
	case <-time.After(30 * time.Second):
		return cap(t.sem) + 1 // timeout → fail
	}
}

// extractPathFromPrompt finds a [Path] tag in the task prompt (fallback).
func extractPathFromPrompt(prompt string) string {
	idx := strings.Index(prompt, "[Path]")
	if idx < 0 {
		// Try "path:" style.
		idx = strings.Index(prompt, "Path:")
		if idx < 0 {
			return ""
		}
		rest := prompt[idx+len("Path:"):]
		end := strings.IndexAny(rest, "\n[")
		if end < 0 {
			return strings.TrimSpace(rest)
		}
		return strings.TrimSpace(rest[:end])
	}
	rest := prompt[idx+len("[Path]"):]
	end := strings.IndexAny(rest, "\n[")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}
