package eventpipe

import (
	"fmt"
	"sync/atomic"

	"github.com/NB-Agent/ok/internal/event"
)

// Eventizer adapts the old event.Sink contract to the typed eventpipe.Sink.
// It implements event.Sink, converts each old-style Event to its typed
// counterpart, and forwards it to the typed pipeline.
//
// Usage:
//
//	pipe := FanOut(logSink, uiSink)
//	ez := eventpipe.NewEventizer(pipe)
//	ctrl := control.New(control.Options{Sink: ez, ...})
//
// The agent emits old-style events; the Eventizer translates them to typed
// events and feeds the pipeline. Zero agent code changes.
type Eventizer struct {
	counter int32 // atomic counter for event IDs
	next    Sink  // typed pipeline sink
}

// NewEventizer wraps a typed pipeline sink with the old-style adapter.
func NewEventizer(next Sink) *Eventizer {
	if next == nil {
		next = Discard
	}
	return &Eventizer{next: next}
}

// allocID returns a monotonically increasing event ID.
func (ez *Eventizer) allocID() int {
	return int(atomic.AddInt32(&ez.counter, 1)) - 1
}

// Emit implements event.Sink. It translates the old flat event to a typed
// eventpipe.Event and forwards it to the pipeline.
func (ez *Eventizer) Emit(ev *event.Event) {
	if ev == nil {
		return
	}
	typed := ez.convert(ev)
	if typed != nil {
		ez.next.Emit(typed)
	}
}

// convert translates one old-style event to a typed event, or returns nil
// for events that should be dropped.
func (ez *Eventizer) convert(ev *event.Event) Event {
	id := ez.allocID()

	switch ev.Kind {
	case event.TurnStarted:
		return NewPhaseEvent(0, id, "turn started")

	case event.Reasoning:
		return NewModelDeltaEvent(0, id, "reasoning", ev.Text)

	case event.Text:
		return NewModelDeltaEvent(0, id, "content", ev.Text)

	case event.Message:
		var u Usage
		if ev.Usage != nil {
			u = Usage{
				PromptTokens:     ev.Usage.PromptTokens,
				CompletionTokens: ev.Usage.CompletionTokens,
				TotalTokens:      ev.Usage.TotalTokens,
				CacheHitTokens:   ev.SessionHit,
				CacheMissTokens:  ev.SessionMiss,
			}
		}
		return NewModelFinalEvent(0, id, ev.Text, ev.Reasoning, u, 0)

	case event.ToolDispatch:
		t := ev.Tool
		if t.Partial {
			return NewToolPreparingEvent(0, id, t.ID, t.Name)
		}
		return NewToolIntentEvent(0, id, t.ID, t.Name, t.Args, t.ReadOnly, t.ParentID)

	case event.ToolResult:
		t := ev.Tool
		if t.Err != "" {
			return NewToolDeniedEvent(0, id, t.ID, t.Name, t.Err)
		}
		return NewToolResultEvent(0, id, t.ID, t.Name, true, t.Output, "", t.Truncated, 0)

	case event.Usage:
		return nil // folded into ModelFinalEvent

	case event.Notice:
		lvl := NoticeLevelInfo
		if ev.Level == event.LevelWarn {
			lvl = NoticeLevelWarn
		}
		return NewNoticeEvent(0, id, ev.Text, lvl)

	case event.Phase:
		return NewPhaseEvent(0, id, ev.Text)

	case event.ApprovalRequest:
		a := ev.Approval
		return NewApprovalRequestEvent(0, id, a.ID, a.Tool, a.Subject)

	case event.AskRequest:
		ask := ev.Ask
		questions := make([]AskQuestion, len(ask.Questions))
		for i, q := range ask.Questions {
			opts := make([]AskOption, len(q.Options))
			for j, o := range q.Options {
				opts[j] = AskOption{Label: o.Label, Description: o.Description}
			}
			questions[i] = AskQuestion{
				ID: q.ID, Header: q.Header, Prompt: q.Prompt,
				Options: opts, Multi: q.Multi,
			}
		}
		return NewAskRequestEvent(0, id, ask.ID, questions)

	case event.TurnDone:
		var text string
		if ev.Err != nil {
			text = ev.Err.Error()
		}
		done := NewTurnDoneEvent(0, id, ev.Err)
		done.Text = text
		return done

	case event.TurnAborted:
		return NewTurnAbortedEvent(0, id, ev.Err, ev.Covenant,
			fmt.Sprintf("covenant violation: %s", ev.Covenant))

	default:
		return nil
	}
}

// Ensure compile-time compatibility.
var _ event.Sink = (*Eventizer)(nil)

// NewEventizerSink creates a sink that accepts old events and forwards
// typed events to the pipeline. Useful as a drop-in for control.New().
func NewEventizerSink(pipeline Sink) *EventizerSink {
	return &EventizerSink{Eventizer: NewEventizer(pipeline)}
}

// EventizerSink is a drop-in event.Sink that translates to typed events.
type EventizerSink struct {
	*Eventizer
}

// LegacySink adapts an eventpipe.Sink to the old event.Sink interface.
type LegacySink struct {
	ez *Eventizer
}

func NewLegacySink(pipeline Sink) *LegacySink {
	return &LegacySink{ez: NewEventizer(pipeline)}
}

func (s *LegacySink) Emit(ev *event.Event) {
	s.ez.Emit(ev)
}
