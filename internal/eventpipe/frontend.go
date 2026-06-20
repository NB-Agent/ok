package eventpipe

import (
	"fmt"
	"sync"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
)

// FrontendBridge implements eventpipe.Sink by converting typed events back to
// the old event.Event format and forwarding to an old-style event.Sink.
// This lets frontends consume typed events through the pipeline without
// changing their existing ingest/emit code.
//
// Session-cumulative cache tokens (SessionHit/SessionMiss) are tracked
// automatically across ModelFinalEvent emissions so the old-style event
// carries both per-turn and cumulative values.
type FrontendBridge struct {
	mu       sync.Mutex
	oldSink  event.Sink
	sessHit  int
	sessMiss int
}

// NewFrontendBridge wraps an old event.Sink so it can be used in the typed
// pipeline. The old sink receives converted old-style events.
func NewFrontendBridge(old event.Sink) Sink {
	if old == nil {
		return Discard
	}
	return &FrontendBridge{oldSink: old}
}

// Emit converts a typed event to old format and forwards it.
func (b *FrontendBridge) Emit(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if old := toOldEvent(b, ev); old != nil {
		b.oldSink.Emit(old)
	}
}

// toOldEvent converts a typed eventpipe.Event back to the old event.Event.
// The bridge argument provides session-cumulative cache values; nil means
// no accumulation (SessionHit/SessionMiss remain 0).
func toOldEvent(b *FrontendBridge, ev Event) *event.Event {
	switch e := ev.(type) {
	case UserMessageEvent:
		return &event.Event{Kind: event.Text, Text: e.Text}

	case ModelTurnStartedEvent:
		return &event.Event{Kind: event.TurnStarted}

	case ModelDeltaEvent:
		switch e.Channel {
		case "reasoning":
			return &event.Event{Kind: event.Reasoning, Text: e.Text}
		default:
			return &event.Event{Kind: event.Text, Text: e.Text}
		}

	case ModelFinalEvent:
		if b != nil {
			b.sessHit += e.Usage.CacheHitTokens
			b.sessMiss += e.Usage.CacheMissTokens
		}
		hit, miss := 0, 0
		if b != nil {
			hit, miss = b.sessHit, b.sessMiss
		}
		return &event.Event{
			Kind:      event.Message,
			Text:      e.Content,
			Reasoning: e.ReasoningContent,
			Usage: &provider.Usage{
				PromptTokens:     e.Usage.PromptTokens,
				CompletionTokens: e.Usage.CompletionTokens,
				TotalTokens:      e.Usage.TotalTokens,
				CacheHitTokens:   e.Usage.CacheHitTokens,
				CacheMissTokens:  e.Usage.CacheMissTokens,
			},
			SessionHit:  hit,
			SessionMiss: miss,
		}

	case ToolPreparingEvent:
		return &event.Event{
			Kind: event.ToolDispatch,
			Tool: event.Tool{ID: e.CallID, Name: e.Name, Partial: true},
		}

	case ToolIntentEvent:
		return &event.Event{
			Kind: event.ToolDispatch,
			Tool: event.Tool{
				ID: e.CallID, Name: e.Name, Args: e.Args,
				ReadOnly: e.ReadOnly, ParentID: e.ParentID,
			},
		}

	case ToolDeniedEvent:
		return &event.Event{
			Kind: event.ToolResult,
			Tool: event.Tool{ID: e.CallID, Name: e.Name, Err: e.Reason},
		}

	case ToolResultEvent:
		return &event.Event{
			Kind: event.ToolResult,
			Tool: event.Tool{
				ID: e.CallID, Name: e.Name, Output: e.Output,
				Truncated: e.Truncated,
			},
		}

	case StatusEvent:
		return &event.Event{Kind: event.Notice, Text: e.Text, Level: event.LevelInfo}

	case ErrorEvent:
		return &event.Event{Kind: event.Notice, Text: e.Message, Level: event.LevelWarn}

	case NoticeEvent:
		lvl := event.LevelInfo
		if e.Level == NoticeLevelWarn {
			lvl = event.LevelWarn
		}
		return &event.Event{Kind: event.Notice, Text: e.Text, Level: lvl}

	case PhaseEvent:
		return &event.Event{Kind: event.Phase, Text: e.Text}

	case ApprovalRequestEvent:
		return &event.Event{
			Kind:     event.ApprovalRequest,
			Approval: event.Approval{ID: e.AskID, Tool: e.Tool, Subject: e.Subject},
		}

	case AskRequestEvent:
		questions := make([]event.AskQuestion, len(e.Questions))
		for i, q := range e.Questions {
			opts := make([]event.AskOption, len(q.Options))
			for j, o := range q.Options {
				opts[j] = event.AskOption(o)
			}
			questions[i] = event.AskQuestion{
				ID: q.ID, Header: q.Header, Prompt: q.Prompt,
				Options: opts, Multi: q.Multi,
			}
		}
		return &event.Event{
			Kind: event.AskRequest,
			Ask:  event.Ask{ID: e.AskID, Questions: questions},
		}

	case TurnDoneEvent:
		return &event.Event{Kind: event.TurnDone, Err: e.Err}

	case TurnAbortedEvent:
		msg := e.Reason
		if msg == "" {
			msg = "covenant violation: " + e.Covenant
		}
		return &event.Event{Kind: event.TurnDone, Err: fmt.Errorf("%s", msg)}

	case PlanSubmittedEvent:
		return &event.Event{Kind: event.Notice, Text: "plan: " + e.Body, Level: event.LevelInfo}

	case PlanStepChangedEvent:
		return &event.Event{Kind: event.Notice, Text: "plan step: " + e.Status + " " + e.StepID, Level: event.LevelInfo}

	case BudgetWarningEvent:
		return &event.Event{Kind: event.Notice, Text: fmt.Sprintf("budget: $%.4f / $%.2f", e.SpentUSD, e.CapUSD), Level: event.LevelWarn}

	case BudgetBlockedEvent:
		return &event.Event{Kind: event.Notice, Text: fmt.Sprintf("budget BLOCKED: $%.4f / $%.2f", e.SpentUSD, e.CapUSD), Level: event.LevelWarn}

	case SessionOpenedEvent:
		return &event.Event{Kind: event.Notice, Text: "session: " + e.Name, Level: event.LevelInfo}

	case SessionCompactedEvent:
		return &event.Event{Kind: event.Notice, Text: fmt.Sprintf("compacted: %d->%d msgs", e.BeforeMessages, e.AfterMessages), Level: event.LevelInfo}

	default:
		return nil
	}
}
