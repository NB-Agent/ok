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
	if a.evidenceLedger != nil {
		a.evidenceLedger.Reset()
	}

	for step := 0; a.maxSteps <= 0 || step < a.maxSteps; step++ {
		text, reasoning, calls, usage, err := a.stream(ctx)
		if err != nil {
			return err
		}
		// Usage and token telemetry are folded into ModelFinalEvent by stream.go
		// when the pipe is active. When using the old sink, we emit separately.
		if pipe == nil && usage != nil && usage.TotalTokens > 0 {
			// Cache diagnostics have been captured in streamOnce via
			// a.lastPrefixShape / a.haveLastPrefixShape; read the latest.
			cacheDiag := event.CacheDiagnostics{}
			if a.haveLastPrefixShape {
				cacheDiag = CompareShape(a.lastPrefixShape, a.lastPrefixShape, usage)
			}
			hit, miss := a.usage.SessionCache()
			a.sink.Emit(&event.Event{Kind: event.Usage, Usage: usage, Pricing: a.pricing,
				SessionHit: hit, SessionMiss: miss,
				CacheDiagnostics: &cacheDiag})
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

// preStreamCompact removed (2026-06): the strategy of compacting BEFORE a stream
// call guarantees a cache miss on the next API call, which costs more than a
// single slow call on a large context. maybeCompact handles compaction after
// the stream with real usage data — one trigger is enough.
