package agent

import (
	"context"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/eventpipe"
	"github.com/NB-Agent/ok/internal/provider"
)

// stream runs one completion, emitting reasoning and text deltas as typed
// events and collecting complete tool calls. Retries once on transient stream
// errors (the provider already handles HTTP-level retries internally, so
// this only catches mid-stream corruption).
func (a *Agent) stream(ctx context.Context) (string, string, []provider.ToolCall, *provider.Usage, error) {
	const maxStreamRetries = 4
	for attempt := 0; attempt <= maxStreamRetries; attempt++ {
		if attempt > 0 {
			d := 1 << uint(attempt)
			if d > 30 {
				d = 30
			}
			delay := time.Duration(d) * time.Second
			select {
			case <-ctx.Done():
				return "", "", nil, nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		text, reasoning, calls, usage, err := a.streamOnce(ctx)
		if err == nil || attempt == maxStreamRetries {
			return text, reasoning, calls, usage, err
		}
		if !isTransientStreamErr(err) {
			return "", "", nil, nil, err
		}
	}
	panic("unreachable")
}

func (a *Agent) streamOnce(ctx context.Context) (string, string, []provider.ToolCall, *provider.Usage, error) {
	// Capture prefix shape before API call for cache diagnostics.
	schemas := a.tools.Schemas()
	prefixShape := a.capturePrefixShape(schemas)

	msgs := a.session.Snapshot()
	ch, err := a.prov.Stream(ctx, provider.Request{
		Messages:    msgs,
		Tools:       schemas,
		Temperature: a.temperature,
	})
	if err != nil {
		return "", "", nil, nil, err
	}

	var text, reasoning strings.Builder
	var calls []provider.ToolCall
	var usage *provider.Usage
	var idCounter int // local event ID counter

	// Prefer typed pipe; fall back to old sink.
	pipe := a.pipeSnapshot()
	usePipe := pipe != nil

	// finalize emits the closing Message event with everything accumulated
	// so far, ensuring partial output is surfaced even on early cancellation.
	finalize := func() {
		if text.Len() > 0 || reasoning.Len() > 0 {
			// Compute cache diagnostics: compare prefix shape before/after.
			prevPrefixShape := a.lastPrefixShape
			if !a.haveLastPrefixShape {
				prevPrefixShape = prefixShape
			}
			cacheDiag := CompareShape(prevPrefixShape, prefixShape, usage)
			a.lastPrefixShape = prefixShape
			a.haveLastPrefixShape = true

			if usePipe {
				u := eventpipe.Usage{}
				cost := 0.0
				if usage != nil {
					u = eventpipe.Usage{
						PromptTokens:     usage.PromptTokens,
						CompletionTokens: usage.CompletionTokens,
						TotalTokens:      usage.TotalTokens,
						CacheHitTokens:   usage.CacheHitTokens,
						CacheMissTokens:  usage.CacheMissTokens,
					}
					if a.pricing != nil {
						cost = a.pricing.Cost(usage)
					}
				}
				ev := eventpipe.NewModelFinalEvent(0, idCounter, text.String(), reasoning.String(), u, cost)
				ev.CacheDiagnostics = &cacheDiag
				pipe.Emit(ev)
			} else {
				a.sink.Emit(&event.Event{Kind: event.Message, Text: text.String(), Reasoning: reasoning.String()})
			}
		}
	}

loop:
	for {
		select {
		case <-ctx.Done():
			finalize()
			return text.String(), reasoning.String(), calls, usage, ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				break loop
			}
			switch chunk.Type {
			case provider.ChunkReasoning:
				reasoning.WriteString(chunk.Text)
				if usePipe {
					pipe.Emit(eventpipe.NewModelDeltaEvent(0, idCounter, "reasoning", chunk.Text))
				} else {
					a.sink.Emit(&event.Event{Kind: event.Reasoning, Text: chunk.Text})
				}
				idCounter++

			case provider.ChunkText:
				text.WriteString(chunk.Text)
				if usePipe {
					pipe.Emit(eventpipe.NewModelDeltaEvent(0, idCounter, "content", chunk.Text))
				} else {
					a.sink.Emit(&event.Event{Kind: event.Text, Text: chunk.Text})
				}
				idCounter++

			case provider.ChunkDone:
				break loop

			default:
				// ChunkToolCallStart, ChunkToolCall, ChunkUsage,
				// ChunkError — handled below.
			case provider.ChunkToolCallStart:
				if tc := chunk.ToolCall; tc != nil {
					if usePipe {
						pipe.Emit(eventpipe.NewToolPreparingEvent(0, idCounter, tc.ID, tc.Name))
					} else {
						a.sink.Emit(&event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
							ID: tc.ID, Name: tc.Name, ReadOnly: a.toolReadOnly(tc.Name), Partial: true,
						}})
					}
					idCounter++
				}

			case provider.ChunkToolCall:
				if chunk.ToolCall != nil {
					calls = append(calls, *chunk.ToolCall)
				}

			case provider.ChunkUsage:
				usage = chunk.Usage
				if chunk.Usage != nil {
					a.usage.Record(*chunk.Usage)
				}

			case provider.ChunkError:
				finalize()
				return text.String(), reasoning.String(), calls, usage, chunk.Err
			}
		}
	}
	finalize()
	return text.String(), reasoning.String(), calls, usage, nil
}

func isTransientStreamErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if strings.Contains(s, "context canceled") || strings.Contains(s, "deadline exceeded") {
		return false
	}
	for _, p := range []string{"429", "500", "502", "503", "504", "timeout", "connection reset", "forcibly closed", "EOF", "broken pipe"} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
