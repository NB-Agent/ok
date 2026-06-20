package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/eventpipe"
	"github.com/NB-Agent/ok/internal/metrics"
	"github.com/NB-Agent/ok/internal/provider"
)

// Run appends the user input and drives the tool loop until the model returns a
// final answer (no tool calls), the context is canceled, or the provider errors.
func (a *Agent) Run(ctx context.Context, input string) error {
	metrics.Turn()
	start := time.Now()
	pipe := a.pipeSnapshot()
	if pipe != nil {
		pipe.Emit(eventpipe.NewPhaseEvent(0, 0, "turn started"))
	} else {
		a.sink.Emit(&event.Event{Kind: event.TurnStarted})
	}
	a.session.Add(provider.Message{Role: provider.RoleUser, Content: input})

	a.preTurnCheck()

	for step := 0; a.maxSteps <= 0 || step < a.maxSteps; step++ {
		// Pre-stream compact: before every API call, if the session is large
		// enough to slow down inference, compact aggressively. This ensures
		// stream() always runs in the fast zone (<55% of context window).
		a.preStreamCompact(ctx)

		startStream := time.Now()
		text, reasoning, calls, usage, err := a.stream(ctx)
		// Track sliding average stream latency so preStreamCompact can adapt
		// to provider-side congestion even at moderate context sizes.
		if latency := time.Since(startStream); latency > 0 {
			if a.streamAvgLatency == 0 {
				a.streamAvgLatency = latency
			} else {
				a.streamAvgLatency = a.streamAvgLatency*7/10 + latency*3/10
			}
		}
		if err != nil {
			return err
		}
		// Usage and token telemetry are folded into ModelFinalEvent by stream.go
		// when the pipe is active. When using the old sink, we emit separately.
		if pipe == nil && usage != nil && usage.TotalTokens > 0 {
			hit, miss := a.usage.SessionCache()
			a.sink.Emit(&event.Event{Kind: event.Usage, Usage: usage, Pricing: a.pricing,
				SessionHit: hit, SessionMiss: miss})
		}
		if msg, ok := finishReasonMessage(usage); ok {
			if pipe != nil {
				pipe.Emit(eventpipe.NewNoticeEvent(0, 0, msg, eventpipe.NoticeLevelWarn))
			} else {
				a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: msg})
			}
		}

		a.session.Add(provider.Message{
			Role:             provider.RoleAssistant,
			Content:          text,
			ReasoningContent: reasoning,
			ToolCalls:        calls,
		})

		if len(calls) == 0 {
			metrics.TurnSucceeded()
			metrics.TurnLatency(time.Since(start))
			a.onTurnCompleteMu.RLock()
			fn := a.onTurnComplete
			a.onTurnCompleteMu.RUnlock()
			if fn != nil {
				fn(ctx, input, text)
			}
			return nil
		}

		results, turnFatal := a.executeBatch(ctx, calls)
		if turnFatal {
			return fmt.Errorf("turn aborted by core covenant")
		}
		for i, call := range calls {
			// Tool-aware context compression (TokenTamer): track file reads
			// and skeletonize stale copies before adding the fresh result.
			msgIndex := a.session.Len() // index this message will have after Add
			priorPaths := a.fileTrack.track(msgIndex, call.Name, results[i])
			if len(priorPaths) > 0 {
				indices := a.fileTrack.priorRefs(priorPaths)
				a.session.SkeletonizeAt(indices, call.Name)
			}
			a.session.Add(provider.Message{
				Role:       provider.RoleTool,
				Content:    results[i],
				ToolCallID: call.ID,
				Name:       call.Name,
			})
		}

		if step > 0 && step%5 == 0 && a.sessionPath != "" {
			if err := a.session.Save(a.sessionPath); err != nil {
				fmt.Fprintf(os.Stderr, "auto-save session: %v\n", err)
			}
		}

		a.maybeCompact(ctx, usage)
	}
	return fmt.Errorf("max_steps reached after %d tool-call rounds (agent.max_steps) — the work so far is saved; send another message to continue, or set max_steps higher or to 0 for no limit", a.maxSteps)
}

// preStreamCompactThreshold is the context-usage fraction at which we compact
// BEFORE the next stream() call. At 55% of a 1M window (550K tokens) the model
// is still fast; above that, inference latency climbs steeply. Compact before
// we pay that tax.
const preStreamCompactThreshold = 0.55

// preStreamCompact checks whether the session has grown too large for a fast
// API response and compacts aggressively BEFORE the next stream() call.
// This is the key fix: never waste a slow API call on a bloated context.
//
// It also considers stream latency: if the model has been responding slowly
// even at modest context sizes (possible provider-side congestion), it
// compacts earlier as a hedge. The combined threshold is:
//
//	compact if contextUsage > 0.55  OR  (contextUsage > 0.40 AND avgLatency > 30s)
func (a *Agent) preStreamCompact(ctx context.Context) {
	if a.contextWindow <= 0 {
		return
	}
	ratio := a.ContextUsage()
	if ratio < preStreamCompactThreshold {
		// Latency-triggered compact: when recent stream calls have been slow
		// even at moderate context sizes, compact preemptively as a hedge
		// against model-side congestion.
		if a.streamAvgLatency <= 30*time.Second || ratio <= 0.40 {
			return
		}
	}
	// Hysteresis: skip if we just compacted (let the prefix stabilize).
	a.compactedLastMu.Lock()
	if a.compactedLast {
		a.compactedLast = false
		a.compactedLastMu.Unlock()
		return
	}
	a.compactedLastMu.Unlock()

	if err := a.AggressiveCompact(ctx); err != nil {
		return // non-fatal; continue with the big context
	}
	a.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
		Text: fmt.Sprintf("pre-stream compact: context was %.0f%% — compressed before API call", ratio*100)})
	if a.msgbus != nil {
		a.msgbus.Pub("turn:compacting", "pre-stream compact")
	}
	a.compactedLastMu.Lock()
	a.compactedLast = true
	a.compactedLastMu.Unlock()
}
