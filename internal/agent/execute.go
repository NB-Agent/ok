package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/metrics"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// executeBatch dispatches one model turn's tool calls. A ToolDispatch event is
// emitted for every call up front, in call order, so a frontend can show the
// timeline chronologically. Calls fan out across goroutines only when every
// call's tool is ReadOnly (allReadOnly); a single non-ReadOnly call drops
// the whole batch back to sequential to preserve write/read ordering. ToolResult
// events are emitted after the batch in call order, so emission stays serial
// even when execution parallelised.
func (a *Agent) executeBatch(ctx context.Context, calls []provider.ToolCall) ([]string, bool) {
	allReadOnly := len(calls) > 1 && canParallelise(a.tools, calls)
	readOnly := make([]bool, len(calls))
	for i, c := range calls {
		t, ok := a.tools.GetAny(c.Name)
		ro := ok && t.ReadOnly()
		readOnly[i] = ro
		a.sink.Emit(&event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
			ID:       c.ID,
			Name:     c.Name,
			Args:     c.Arguments,
			ReadOnly: ro,
		}})
	}

	results := make([]string, len(calls))
	outcomes := make([]toolOutcome, len(calls))
	var fatal atomic.Bool
	var batchCancel context.CancelFunc // set in parallel path; used by run for fatal propagation
	run := func(i int) {
		// Fast path: if the parent context is already cancelled (turn timeout /
		// deadline exceeded), don't bother executing — return immediately.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			outcomes[i] = toolOutcome{
				output: "cancelled: turn timed out",
				errMsg: "turn timed out — this tool call was skipped",
			}
			results[i] = outcomes[i].output
			return
		}
		if ctx.Err() != nil {
			outcomes[i] = toolOutcome{
				output: "cancelled: " + ctx.Err().Error(),
				errMsg: "cancelled",
			}
			results[i] = outcomes[i].output
			return
		}
		outcomes[i] = a.executeOne(ctx, calls[i])
		results[i] = outcomes[i].output
		if outcomes[i].fatal {
			fatal.Store(true)
			if batchCancel != nil {
				batchCancel()
			}
		}
	}

	if allReadOnly {
		// Create a cancellable batch context so a fatal covenant violation in one
		// tool can cancel the others mid-flight (they check ctx.Done()).
		ctx, batchCancel = context.WithCancel(ctx)
		const maxParallel = 8
		sem := make(chan struct{}, maxParallel)
		var wg sync.WaitGroup
		for i := range calls {
			i := i
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				// Context cancelled while waiting for a slot; skip remaining calls.
				outcomes[i] = toolOutcome{output: "cancelled: " + ctx.Err().Error(), errMsg: "cancelled"}
				results[i] = outcomes[i].output
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				defer func() {
					if r := recover(); r != nil {
						log.Error("goroutine panic", "recover", r)
						metrics.Panic()
						fatal.Store(true)
						batchCancel()
						outcomes[i] = toolOutcome{
							output: fmt.Sprintf("panic: %v", r),
							errMsg: fmt.Sprintf("panic: %v", r),
							fatal:  true,
						}
						results[i] = outcomes[i].output
					}
				}()
				// Stop early if another goroutine hit a fatal error or context cancelled.
				if ctx.Err() != nil {
					outcomes[i] = toolOutcome{output: "cancelled: turn timed out", errMsg: "turn cancelled"}
					results[i] = outcomes[i].output
				} else if fatal.Load() {
					if outcomes[i].output == "" {
						outcomes[i] = toolOutcome{output: "skipped: another tool reported a fatal error", errMsg: "cancelled"}
						results[i] = outcomes[i].output
					}
				}
				if ctx.Err() != nil || fatal.Load() {
					// Emit ToolResult so the frontend doesn't show a dangling tool card.
					c := calls[i]
					o := outcomes[i]
					a.sink.Emit(&event.Event{Kind: event.ToolResult, Tool: event.Tool{
						ID: c.ID, Name: c.Name, Args: c.Arguments,
						Output: o.output, Err: o.errMsg, ReadOnly: readOnly[i],
					}})
					return
				}
				run(i)
				// Emit ToolResult as soon as this tool completes (progressive).
				// The Sync sink serializes concurrent emissions safely.
				c := calls[i]
				o := outcomes[i]
				if o.output != "" || o.errMsg != "" {
					a.sink.Emit(&event.Event{Kind: event.ToolResult, Tool: event.Tool{
						ID:        c.ID,
						Name:      c.Name,
						Args:      c.Arguments,
						Output:    o.output,
						Err:       o.errMsg,
						ReadOnly:  readOnly[i],
						Truncated: o.truncated,
					}})
					if o.truncated && o.truncMsg != "" {
						a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: o.truncMsg})
					}
				}
			}()
		}
		wg.Wait()
		batchCancel() // cancel the parallel context to avoid leak
	} else {
		for i := range calls {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Error("sequential tool panic", "recover", r)
						metrics.Panic()
						fatal.Store(true)
						outcomes[i] = toolOutcome{
							output: fmt.Sprintf("panic: %v", r),
							errMsg: fmt.Sprintf("panic: %v", r),
							fatal:  true,
						}
						results[i] = outcomes[i].output
					}
				}()
				run(i)
			}()
			if outcomes[i].fatal {
				break
			}
		}
		// Sequential path: emit results in call order after the batch.
		for i, c := range calls {
			o := outcomes[i]
			if o.output == "" && o.errMsg == "" {
				continue
			}
			a.sink.Emit(&event.Event{Kind: event.ToolResult, Tool: event.Tool{
				ID:        c.ID,
				Name:      c.Name,
				Args:      c.Arguments,
				Output:    o.output,
				Err:       o.errMsg,
				ReadOnly:  readOnly[i],
				Truncated: o.truncated,
			}})
			if o.truncated && o.truncMsg != "" {
				a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: o.truncMsg})
			}
		}
	}
	// Clear the read-only tool cache at batch boundaries — stale results
	// from a previous batch are never safe to reuse across turns.
	a.toolCacheMu.Lock()
	a.toolCache = nil
	a.toolCacheMu.Unlock()
	return results, fatal.Load()
}

// toolOutcome is one tool call's result, split into the model-facing output and
// the display-facing notice bits. errMsg is the short failure reason (empty on
// success) — a refused call, an unknown tool, or an execution error — so a sink
// renders the result as failed ("✗ name <errMsg>" / a red card) instead of OK;
// blocked narrows that to a refusal (plan mode / permission). truncMsg is set
// (without the "· " prefix) when the output was head+tailed.
type toolOutcome struct {
	output    string
	blocked   bool
	errMsg    string
	truncated bool
	truncMsg  string
	fatal     bool   // covenant violation — stop the turn, do not retry
	principle string // covenant principle ID when fatal, e.g. "p2"
}

// auditToolCall records the tool execution in the audit chain.
// It shortens args and result to a reasonable size for the log.
func (a *Agent) auditToolCall(tool, args, result string, allowed bool) {
	if a.auditChain == nil {
		return
	}
	// Limit args to 256 chars for audit readability, respecting UTF-8 rune boundaries.
	shortArgs := args
	if len(shortArgs) > 256 {
		end := 256
		for end > 0 && !utf8.RuneStart(shortArgs[end]) {
			end--
		}
		shortArgs = shortArgs[:end] + "..."
	}
	// Limit result to 512 chars for audit readability.
	shortResult := result
	if len(shortResult) > 512 {
		end := 512
		for end > 0 && !utf8.RuneStart(shortResult[end]) {
			end--
		}
		shortResult = shortResult[:end] + "..."
	}
	a.auditChain.Append(tool, shortArgs, shortResult, allowed)
}

// executeOne runs a single tool call. It is pure with respect to the event sink
// — the caller emits ToolDispatch/ToolResult — so it is safe to invoke from
// parallel goroutines.
func (a *Agent) executeOne(ctx context.Context, call provider.ToolCall) toolOutcome {
	metrics.ToolCall()

	t, ok := a.tools.GetAny(call.Name)
	toolReadOnly := ok && t.ReadOnly()

	// 0: Core Covenant check — absolute first, before any other check.
	// The covenant is compiled into the binary, cannot be overridden by any
	// configuration, gate, or instruction.
	// For read-only tools, skip argument-level scanning (ConflictsWithArgs):
	// they cannot exfiltrate data, so arg scanning only produces false
	// positives with zero security benefit. Name-level check (ConflictsWith)
	// still catches malicious tool names for all tools.
	var p *core.Principle
	if toolReadOnly {
		p = core.DefaultCovenant.ConflictsWith(call.Name)
	} else {
		p = core.DefaultCovenant.ConflictsWithArgs(call.Name, json.RawMessage(call.Arguments))
	}
	if p != nil {
		if a.msgbus != nil {
			a.msgbus.Pub("tool:blocked", ToolMsg{Name: call.Name, Args: call.Arguments, Err: "covenant: " + p.ID})
		}
		msg := fmt.Sprintf("I cannot do this. The action %q violates my core covenant principle %q: %s", call.Name, p.ID, p.Rule)
		a.auditToolCall(call.Name, call.Arguments, "covenant violation - "+p.ID, false)
		a.sink.Emit(&event.Event{Kind: event.TurnAborted, Covenant: p.ID, Err: fmt.Errorf("covenant violation: %s", p.Rule)})
		// p2 (safety) and p5 (integrity) are always fatal — they stop the
		// entire turn. p4 (data sovereignty) is fatal only for write tools:
		// read-only tools cannot exfiltrate data, so p4 blocks only that
		// single tool call. p1 (transparency) and p3 (honesty) block only
		// that tool call.
		fatal := p.ID == "p2" || p.ID == "p5" || (p.ID == "p4" && !toolReadOnly)
		return toolOutcome{
			output:    msg,
			blocked:   true,
			errMsg:    "blocked by core covenant: " + p.ID,
			fatal:     fatal,
			principle: p.ID,
		}
	}

	if !ok {
		if a.msgbus != nil {
			a.msgbus.Pub("tool:blocked", ToolMsg{Name: call.Name, Args: call.Arguments, Err: "unknown tool"})
		}
		a.auditToolCall(call.Name, call.Arguments, "unknown tool", false)
		return toolOutcome{
			output: fmt.Sprintf("error: unknown tool %q", call.Name),
			errMsg: fmt.Sprintf("unknown tool %q", call.Name),
		}
	}
	if a.planMode.Load() && !toolReadOnly {
		if a.msgbus != nil {
			a.msgbus.Pub("tool:blocked", ToolMsg{Name: call.Name, Args: call.Arguments, Err: "plan mode"})
		}
		a.auditToolCall(call.Name, call.Arguments, "blocked: plan mode is read-only", false)
		return toolOutcome{
			output:  fmt.Sprintf("blocked: %q is a writer tool and plan mode is read-only. Keep exploring with read-only tools, then write your plan as your reply — the user will be asked to approve it before any changes are made.", call.Name),
			blocked: true,
			errMsg:  "blocked: plan mode is read-only",
		}
	}
	if g := a.getGate(); g != nil {
		allow, reason, err := g.Check(ctx, call.Name, json.RawMessage(call.Arguments), t.ReadOnly())
		if err != nil {
			if a.msgbus != nil {
				a.msgbus.Pub("tool:blocked", ToolMsg{Name: call.Name, Args: call.Arguments, Err: fmt.Sprintf("gate error: %v", err)})
			}
			a.auditToolCall(call.Name, call.Arguments, "blocked: "+err.Error(), false)
			return toolOutcome{
				output:  fmt.Sprintf("blocked: %s (%v)", reason, err),
				blocked: true,
				errMsg:  fmt.Sprintf("blocked: %v", err),
			}
		}
		if !allow {
			if a.msgbus != nil {
				a.msgbus.Pub("tool:blocked", ToolMsg{Name: call.Name, Args: call.Arguments, Err: "permission denied"})
			}
			a.auditToolCall(call.Name, call.Arguments, "blocked: permission policy - "+reason, false)
			return toolOutcome{
				output:  "blocked: " + reason,
				blocked: true,
				errMsg:  "blocked by permission policy",
			}
		}
	}
	// PreToolUse hooks run after permission is granted but before the call: a
	// gating hook (exit 2) refuses it, surfaced to the model like a gate denial.
	if h := a.getHooks(); h != nil {
		if block, msg := h.PreToolUse(ctx, call.Name, json.RawMessage(call.Arguments)); block {
			if a.msgbus != nil {
				a.msgbus.Pub("tool:blocked", ToolMsg{Name: call.Name, Args: call.Arguments, Err: "PreToolUse hook"})
			}
			if msg == "" {
				msg = "blocked by a PreToolUse hook"
			}
			a.auditToolCall(call.Name, call.Arguments, "blocked: PreToolUse hook - "+msg, false)
			return toolOutcome{
				output:  "blocked: " + msg,
				blocked: true,
				errMsg:  "blocked by PreToolUse hook",
			}
		}
	}
	// Read-only cache: skip re-execution when the same call already ran in this
	// batch. The model often read_file's the same path through multiple chains.
	if t.ReadOnly() {
		key := call.Name + "\x00" + call.Arguments
		a.toolCacheMu.Lock()
		if cached, ok := a.toolCache[key]; ok && cached.ver == a.toolCacheVer {
			a.toolCacheMu.Unlock()
			a.auditToolCall(call.Name, call.Arguments, "cached result", true)
			return toolOutcome{output: cached.data}
		}
		// Initialise the cache on first use (lazy, so a session with zero
		// reads pays nothing). Capped at 256 entries — well above the
		// typical batch size — so a runaway model can't OOM the agent.
		if a.toolCache == nil {
			a.toolCache = make(map[string]toolCacheEntry, 256)
		}
		a.toolCacheMu.Unlock()
	}
	cctx := withCallContext(ctx, call.ID, a.sink, a.getAsker())
	if a.jobs != nil {
		cctx = jobs.WithManager(cctx, a.jobs)
	}
	// Apply a per-call safety timeout so a hung tool (FIFO read, network block)
	// can't stall the entire turn. Background tools (bash run_in_background,
	// task run_in_background) return quickly after spawning — the long-running
	// work happens asynchronously through the jobs manager.
	tctx, cancel := context.WithTimeout(cctx, defaultToolTimeout)
	defer cancel()
	execStart := time.Now()
	result, err := t.Execute(tctx, json.RawMessage(call.Arguments))
	elapsed := time.Since(execStart)
	if a.msgbus != nil {
		e := ""
		if err != nil {
			e = err.Error()
		}
		a.msgbus.Pub("tool:executed", ToolMsg{Name: call.Name, Args: call.Arguments, Result: result, Err: e, Duration: elapsed})
	}
	// PostToolUse hooks observe the result (they can't block); fired whether the
	// call succeeded or errored, since the tool did run.
	if h := a.getHooks(); h != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "agent: PostToolUse panic for tool %q: %v\n", call.Name, r)
				}
			}()
			h.PostToolUse(ctx, call.Name, json.RawMessage(call.Arguments), result)
		}()
		if reason, detail, happened := h.ConsumeRollback(); happened {
			result = fmt.Sprintf("rolled back: %s failed\nerror:\n%s", reason, detail)
		}
	}
	// Writes always invalidate cached reads, even on error (partial write).
	if !t.ReadOnly() {
		a.toolCacheMu.Lock()
		a.toolCacheVer++
		a.toolCacheMu.Unlock()
	}
	if err != nil {
		metrics.ToolError()
		// Wrap raw context deadline errors with the tool name and timeout
		// so the model sees "tool X timed out after 15m0s" instead of the
		// bare "context deadline exceeded".
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("tool %q timed out after %v", call.Name, defaultToolTimeout)
		}
		body, truncMsg := truncateToolOutput(fmt.Sprintf("error: %v\n%s", err, result))
		a.auditToolCall(call.Name, call.Arguments, body, false)
		return toolOutcome{output: body, errMsg: firstLine(err.Error()), truncated: truncMsg != "", truncMsg: truncMsg}
	}
	// Cache read-only results only on success — errors must not be cached,
	// otherwise a retry of the same call would return the stale error as success.
	// Capped at 256 entries to prevent unbounded growth. The capacity is checked
	// under the lock, so concurrent insertions cannot exceed the cap.
	if t.ReadOnly() {
		a.toolCacheMu.Lock()
		if a.toolCache == nil {
			a.toolCache = make(map[string]toolCacheEntry, 256)
		}
		if len(a.toolCache) < 256 {
			cached, _ := truncateToolOutput(result)
			a.toolCache[call.Name+"\x00"+call.Arguments] = toolCacheEntry{data: cached, ver: a.toolCacheVer}
		}
		// Evict one entry when the cache exceeds 256 due to concurrent
		// insertion, keeping the cap enforcement eventual instead of strict.
		if len(a.toolCache) >= 512 {
			for k := range a.toolCache {
				delete(a.toolCache, k)
				break
			}
		}
		a.toolCacheMu.Unlock()
	}
	body, truncMsg := truncateToolOutput(result)
	metrics.ToolResult()
	a.auditToolCall(call.Name, call.Arguments, body, true)
	return toolOutcome{output: body, truncated: truncMsg != "", truncMsg: truncMsg}
}

// toolReadOnly reports a tool's ReadOnly classification by name (false for an
// unknown tool), for stamping early ToolDispatch events.
func (a *Agent) toolReadOnly(name string) bool {
	t, ok := a.tools.GetAny(name)
	return ok && t.ReadOnly()
}

// canParallelise returns true iff every call targets a known, ReadOnly tool.
// Any unknown tool name (let the sequential path produce a clean error) or any
// non-ReadOnly tool (preserve write ordering) forces serial execution.
func canParallelise(r tool.ToolRegistry, calls []provider.ToolCall) bool {
	for _, c := range calls {
		t, ok := r.GetAny(c.Name)
		if !ok || !t.ReadOnly() {
			return false
		}
	}
	return true
}
