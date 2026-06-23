package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/memory"
	"github.com/NB-Agent/ok/internal/provider"
)

// --- turn lifecycle ---

// PlanApprovalTool is the Tool name the controller uses on the ApprovalRequest
// it emits to gate a proposed plan. Frontends key their plan-approval UI on it.
const PlanApprovalTool = "exit_plan_mode"

// runGuarded runs body on a background goroutine under a fresh cancellable
// context, guarding against concurrent turns and emitting a TurnDone event when
// it finishes. A no-op if a turn is already in flight.
func (c *Controller) runGuarded(body func(ctx context.Context) error) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		c.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo,
			Text: "a previous turn is still running — wait for it to finish"})
		return
	}
	// 60-minute round-level timeout so a deep turn (sub-agents, multi-tool
	// chains, large context) can run to completion. Individual tool calls are
	// capped at defaultToolTimeout (15 min); pre-stream compact keeps the
	// context lean so API calls average <20s — the outer safety net is only
	// for truly degenerate cases.
	ctx, cancel := context.WithTimeout(c.baseCtx, 60*time.Minute)
	c.cancel = cancel
	c.running = true
	c.mu.Unlock()

	go func() {
		defer cancel()
		var panicked bool
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				var err error
				if e, ok := r.(error); ok {
					err = fmt.Errorf("turn panic: %w", e)
				} else {
					err = fmt.Errorf("turn panic: %v", r)
				}
				log.Error("turn panic", "stack", string(debug.Stack()))
				c.mu.Lock()
				c.running = false
				c.cancel = nil
				c.mu.Unlock()
				c.sink.Emit(&event.Event{Kind: event.TurnDone, Err: err})
				if c.msgbus != nil {
					c.msgbus.Pub("turn:done", err)
				}
			}
		}()
		if c.msgbus != nil {
			c.msgbus.Pub("turn:started", nil)
		}
		err := body(ctx)
		if panicked {
			return // already emitted TurnDone in the recover path
		}
		c.mu.Lock()
		c.running = false
		c.cancel = nil
		c.mu.Unlock()
		c.sink.Emit(&event.Event{Kind: event.TurnDone, Err: err})
		if c.msgbus != nil {
			c.msgbus.Pub("turn:done", err)
		}
	}()
}

// Send starts a turn with an already-composed message (the caller applied any
// plan-mode marker and @-ref expansion). Used by the chat TUI.
func (c *Controller) Send(input string) {
	c.runGuarded(func(ctx context.Context) error { return c.runTurn(ctx, input) })
}

// runTurn runs one model turn with no plan-approval gate — the agent always
// executes in full-automation (yolo) mode.
func (c *Controller) runTurn(ctx context.Context, input string) error {
	// Pre-turn context check: proactively compact when nearing the limit
	// instead of waiting for a timeout. This prevents "context deadline
	// exceeded" by keeping the prompt small enough for fast model responses.
	if c.executor != nil {
		if err := c.executor.CheckContextOverflow(); err != nil {
			c.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
				Text: err.Error()})
			// Hard overflow — must compact before we can continue.
			if aggErr := c.executor.AggressiveCompact(ctx); aggErr != nil {
				return err // can't recover
			}
		}
	}
	// Increment turn counter for every turn, regardless of hooks.
	// Used by Compose for periodic env diagnosis injection.
	c.mu.Lock()
	c.turn++
	turn := c.turn
	c.mu.Unlock()
	// Begin a checkpoint for this turn before any tool writes fire.
	if c.cp != nil {
		c.cp.Begin(turn, input, 0)
	}

	if c.hooks.Enabled() {
		if block, msg := c.hooks.PromptSubmit(ctx, input, turn); block {
			if msg != "" {
				fmt.Fprintf(os.Stderr, "control: prompt blocked by hook: %s\n", msg)
			}
			return nil // the hook's notify callback already surfaced the reason
		}
		// Stop hook fires when the turn ends (defer), but only if PromptSubmit
		// didn't block — a blocked prompt never started a turn, so no Stop.
		defer func() { c.hooks.Stop(ctx, lastAssistantText(c.History()), turn) }()
	}
	// Deferred auto-save: always save even on timeout/error.
	defer func() {
		if path := c.SessionPath(); path != "" && c.executor != nil {
			if saveErr := c.executor.Session().Save(path); saveErr != nil {
				c.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
					Text: "auto-save failed: " + saveErr.Error()})
			}
		}
	}()

	// Session-goal auto-save to OK.md was removed (2026-06).
	// It caused unbounded prefix bloat (~265 entries, ~40KB) because every turn
	// appended to the doc-memory file loaded in the system prompt, making every
	// session slower. Session crash recovery is handled by the auto-save above
	// (executor.Session().Save). If you want to persist a session goal, use
	// `#<note>` or `remember` explicitly.

	// Apply any pending tool-group switch from the previous turn so the
	// new schemas take effect at a clean turn boundary. Deferring the
	// switch keeps tool schemas byte-stable within each turn, preserving
	// DeepSeek's prefix cache across consecutive API calls.
	if c.reg != nil {
		c.reg.ApplyPendingGroups()
	}

	if err := c.runner.Run(ctx, input); err != nil {
		if errors.Is(err, context.DeadlineExceeded) && c.executor != nil {
			c.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
				Text: "turn timed out — state saved. Compact suggested before next turn."})
			_ = c.executor.AggressiveCompact(ctx)
		}
		return err
	}
	return nil
}

// Submith is the one-call entry for a simple frontend: it takes raw user input
// and does everything — slash-command dispatch, @-reference expansion, plan-mode
// composition — emitting all output as events.
func (c *Controller) Submit(input string) {
	trimmed := strings.TrimSpace(input)
	switch {
	case trimmed == "/compact":
		c.mu.Lock()
		if c.running {
			c.mu.Unlock()
			c.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
				Text: "cannot compact while a turn is running — wait for it to finish"})
			return
		}
		c.mu.Unlock()
		c.bgWG.Add(1)
		go func() {
			defer c.bgWG.Done()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "control: panic in compact: %v\n", r)
				}
			}()
			ctx, cancel := context.WithTimeout(c.baseCtx, 30*time.Second)
			defer cancel()
			if err := c.Compact(ctx); err != nil {
				c.notice("compaction failed: " + err.Error())
			} else {
				c.notice("compacted")
				if err := c.Snapshot(); err != nil {
					c.notice("snapshot after compact failed: " + err.Error())
				}
			}
		}()
	case trimmed == "/new":
		c.mu.Lock()
		if c.running {
			c.mu.Unlock()
			c.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
				Text: "cannot start new session while a turn is running — wait for it to finish"})
			return
		}
		c.mu.Unlock()
		c.bgWG.Add(1)
		go func() {
			defer c.bgWG.Done()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "control: panic in new session: %v\n", r)
				}
			}()
			if err := c.NewSession(); err != nil {
				c.notice("new session failed: " + err.Error())
			} else {
				c.notice("new session")
			}
		}()
	case strings.HasPrefix(trimmed, "#"):
		note := strings.TrimSpace(trimmed[1:])
		if note == "" {
			c.notice("nothing to remember")
			return
		}
		if path, err := c.QuickAdd(memory.ScopeProject, note); err != nil {
			c.notice("memory: " + err.Error())
		} else {
			c.notice("remembered — " + path)
		}
	case trimmed == "/dst on":
		if !c.IsDSTAvailable() {
			c.notice("DST: not available (hooks not initialized)")
		} else {
			c.SetDSTEnabled(true)
			c.notice("DST: on — per-step verification active")
		}
	case trimmed == "/dst off":
		c.SetDSTEnabled(false)
		c.notice("DST: off — per-step verification disabled")
	case trimmed == "/dst status":
		if c.DSTEnabled() {
			c.notice("DST: on — compile/test checks + proof chain active")
		} else {
			c.notice("DST: off")
		}
	case strings.HasPrefix(trimmed, "/dst run"):
		req := strings.TrimSpace(strings.TrimPrefix(trimmed, "/dst run"))
		if req == "" {
			c.notice("DST: usage: /dst run <requirement>")
			return
		}
		c.notice(fmt.Sprintf("DST: compile/test checks active — verifying: %s", req))
	case trimmed == "/audit":
		c.showAudit()
	case strings.HasPrefix(trimmed, "/search"):
		c.showSearch(strings.TrimPrefix(trimmed, "/search "))
	case strings.HasPrefix(trimmed, "/permissions"):
		c.handlePermissions(trimmed)
	case trimmed == "/cancel":
		c.Cancel()
		c.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo,
			Text: "turn cancelled"})
	case strings.HasPrefix(trimmed, "/mcp__"):
		c.runGuarded(func(ctx context.Context) error {
			sent, found, err := c.MCPPrompt(ctx, trimmed)
			if err != nil {
				return err
			}
			if !found {
				c.notice("unknown command: " + trimmed)
				return nil
			}
			return c.runner.Run(ctx, c.Compose(sent))
		})
	case strings.HasPrefix(trimmed, "/"):
		if c.managementNotice(trimmed) {
			return
		}
		if sent, ok := c.CustomCommand(trimmed); ok {
			c.runGuarded(func(ctx context.Context) error {
				return c.runTurn(ctx, c.Compose(sent))
			})
			return
		}
		if sent, ok := c.RunSkill(trimmed); ok {
			c.runGuarded(func(ctx context.Context) error {
				return c.runTurn(ctx, c.Compose(sent))
			})
			return
		}
		c.notice("unknown command: " + trimmed)
	default:
		c.runGuarded(func(ctx context.Context) error {
			block, errs := c.ResolveRefs(ctx, input)
			for _, e := range errs {
				c.notice(e)
			}
			sent := input
			if block != "" {
				sent = block + "\n\n" + input
			}
			return c.runTurn(ctx, c.Compose(sent))
		})
	}
}

// lastAssistantText returns the content of the most recent assistant message with
// non-empty text — the model's final answer for the turn (its plan, in plan mode).
func lastAssistantText(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleAssistant && strings.TrimSpace(msgs[i].Content) != "" {
			return msgs[i].Content
		}
	}
	return ""
}
