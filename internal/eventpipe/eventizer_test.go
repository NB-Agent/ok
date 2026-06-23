package eventpipe

import (
	"testing"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
)

func eventPtr(k event.Kind, opts ...func(*event.Event)) *event.Event {
	e := &event.Event{Kind: k}
	for _, o := range opts {
		o(e)
	}
	return e
}

func text(text string) func(*event.Event) {
	return func(e *event.Event) { e.Text = text }
}

func level(l event.Level) func(*event.Event) {
	return func(e *event.Event) { e.Level = l }
}

func withTool(id, name, args string, readOnly, partial bool, parentID string) func(*event.Event) {
	return func(e *event.Event) {
		e.Tool = event.Tool{
			ID: id, Name: name, Args: args, ReadOnly: readOnly,
			Partial: partial, ParentID: parentID,
		}
	}
}

func withResult(id, name, output, errMsg string, truncated bool) func(*event.Event) {
	return func(e *event.Event) {
		e.Tool = event.Tool{ID: id, Name: name, Output: output, Err: errMsg, Truncated: truncated}
	}
}

func TestEventizerReasoning(t *testing.T) {
	var got []Event
	ez := NewEventizer(FuncSink(func(ev Event) { got = append(got, ev) }))
	ez.Emit(eventPtr(event.Reasoning, text("thinking...")))
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	delta, ok := got[0].(ModelDeltaEvent)
	if !ok {
		t.Fatalf("type = %T", got[0])
	}
	if delta.Channel != "reasoning" || delta.Text != "thinking..." {
		t.Errorf("delta = %+v", delta)
	}
}

func TestEventizerText(t *testing.T) {
	var got []Event
	ez := NewEventizer(FuncSink(func(ev Event) { got = append(got, ev) }))
	ez.Emit(eventPtr(event.Text, text("Hello")))
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	delta, ok := got[0].(ModelDeltaEvent)
	if !ok {
		t.Fatalf("type = %T", got[0])
	}
	if delta.Channel != "content" || delta.Text != "Hello" {
		t.Errorf("delta = %+v", delta)
	}
}

func TestEventizerMessage(t *testing.T) {
	var got []Event
	ez := NewEventizer(FuncSink(func(ev Event) { got = append(got, ev) }))
	ez.Emit(&event.Event{
		Kind:      event.Message,
		Text:      "Hello world",
		Reasoning: "thinking step",
		Usage: &provider.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
		SessionHit:  80,
		SessionMiss: 20,
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	final, ok := got[0].(ModelFinalEvent)
	if !ok {
		t.Fatalf("type = %T", got[0])
	}
	if final.Content != "Hello world" || final.ReasoningContent != "thinking step" {
		t.Errorf("content/reasoning = %q / %q", final.Content, final.ReasoningContent)
	}
	if final.Usage.PromptTokens != 100 || final.Usage.CompletionTokens != 50 {
		t.Errorf("usage = %+v", final.Usage)
	}
}

func TestEventizerToolDispatch(t *testing.T) {
	var got []Event
	ez := NewEventizer(FuncSink(func(ev Event) { got = append(got, ev) }))

	// Partial dispatch → ToolPreparing
	ez.Emit(eventPtr(event.ToolDispatch, withTool("c1", "read_file", "", true, true, "")))
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if _, ok := got[0].(ToolPreparingEvent); !ok {
		t.Fatalf("expected ToolPreparingEvent, got %T", got[0])
	}
	prep := got[0].(ToolPreparingEvent)
	if prep.CallID != "c1" || prep.Name != "read_file" {
		t.Errorf("prep = %+v", prep)
	}

	// Full dispatch → ToolIntent
	ez.Emit(eventPtr(event.ToolDispatch, withTool("c1", "read_file", `{"path":"x"}`, true, false, "")))
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	intent, ok := got[1].(ToolIntentEvent)
	if !ok {
		t.Fatalf("expected ToolIntentEvent, got %T", got[1])
	}
	if intent.CallID != "c1" || intent.Args != `{"path":"x"}` || !intent.ReadOnly {
		t.Errorf("intent = %+v", intent)
	}
}

func TestEventizerToolResult(t *testing.T) {
	var got []Event
	ez := NewEventizer(FuncSink(func(ev Event) { got = append(got, ev) }))

	// Error → ToolDenied
	ez.Emit(eventPtr(event.ToolResult, withResult("c1", "bash", "", "blocked by policy", false)))
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	denied, ok := got[0].(ToolDeniedEvent)
	if !ok {
		t.Fatalf("expected ToolDeniedEvent, got %T", got[0])
	}
	if denied.Reason != "blocked by policy" {
		t.Errorf("denied reason = %q", denied.Reason)
	}

	// OK → ToolResult
	ez.Emit(eventPtr(event.ToolResult, withResult("c2", "read_file", "content", "", false)))
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	res, ok := got[1].(ToolResultEvent)
	if !ok {
		t.Fatalf("expected ToolResultEvent, got %T", got[1])
	}
	if !res.OK || res.Output != "content" {
		t.Errorf("result = %+v", res)
	}
}

func TestEventizerNotice(t *testing.T) {
	var got []Event
	ez := NewEventizer(FuncSink(func(ev Event) { got = append(got, ev) }))

	ez.Emit(eventPtr(event.Notice, text("hello"), level(event.LevelInfo)))
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	n, ok := got[0].(NoticeEvent)
	if !ok || n.Level != NoticeLevelInfo || n.Text != "hello" {
		t.Errorf("notice = %+v", n)
	}

	ez.Emit(eventPtr(event.Notice, text("warning"), level(event.LevelWarn)))
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	n2 := got[1].(NoticeEvent)
	if n2.Level != NoticeLevelWarn {
		t.Errorf("level = %d", n2.Level)
	}
}

func TestEventizerTurnDone(t *testing.T) {
	var got []Event
	ez := NewEventizer(FuncSink(func(ev Event) { got = append(got, ev) }))

	ez.Emit(eventPtr(event.TurnDone))
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if _, ok := got[0].(TurnDoneEvent); !ok {
		t.Fatalf("expected TurnDoneEvent, got %T", got[0])
	}
}

func TestEventizerTurnAborted(t *testing.T) {
	var got []Event
	ez := NewEventizer(FuncSink(func(ev Event) { got = append(got, ev) }))

	ez.Emit(&event.Event{Kind: event.TurnAborted, Covenant: "p2", Err: nil})
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	aborted, ok := got[0].(TurnAbortedEvent)
	if !ok {
		t.Fatalf("expected TurnAbortedEvent, got %T", got[0])
	}
	if aborted.Covenant != "p2" {
		t.Errorf("covenant = %q", aborted.Covenant)
	}
}

func TestEventizerPhase(t *testing.T) {
	var got []Event
	ez := NewEventizer(FuncSink(func(ev Event) { got = append(got, ev) }))

	ez.Emit(eventPtr(event.Phase, text("planning")))
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	p, ok := got[0].(PhaseEvent)
	if !ok || p.Text != "planning" {
		t.Errorf("phase = %+v", p)
	}
}

func TestEventizerAskRequest(t *testing.T) {
	var got []Event
	ez := NewEventizer(FuncSink(func(ev Event) { got = append(got, ev) }))

	ez.Emit(&event.Event{
		Kind: event.AskRequest,
		Ask: event.Ask{
			ID: "ask-1",
			Questions: []event.AskQuestion{
				{
					ID: "q1", Header: "Pick", Prompt: "Which?",
					Options: []event.AskOption{{Label: "A"}, {Label: "B", Description: "desc"}},
					Multi:   false,
				},
			},
		},
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	ask, ok := got[0].(AskRequestEvent)
	if !ok {
		t.Fatalf("expected AskRequestEvent, got %T", got[0])
	}
	if ask.AskID != "ask-1" || len(ask.Questions) != 1 {
		t.Errorf("ask = %+v", ask)
	}
	if ask.Questions[0].Options[1].Description != "desc" {
		t.Errorf("option desc = %q", ask.Questions[0].Options[1].Description)
	}
}

func TestEventizerUsageDropped(t *testing.T) {
	var got []Event
	ez := NewEventizer(FuncSink(func(ev Event) { got = append(got, ev) }))

	ez.Emit(&event.Event{Kind: event.Usage, Usage: &provider.Usage{PromptTokens: 100}})
	if len(got) != 0 {
		t.Errorf("expected 0 events, got %d", len(got))
	}
}

func TestEventizerNil(t *testing.T) {
	ez := NewEventizer(nil)
	ez.Emit(nil)                                   // nil event → noop
	ez.Emit(&event.Event{Kind: event.TurnStarted}) // nil pipeline → Discard
}

func TestEventizerPipeline(t *testing.T) {
	// Full pipeline: Eventizer → LogSink → Reducer replay
	dir := t.TempDir()
	logSink, err := NewLogSink(LogConfig{Dir: dir, SessionID: "ez-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer logSink.Close()

	var typed []Event
	collector := FuncSink(func(ev Event) { typed = append(typed, ev) })
	pipeline := FanOut(logSink, collector)
	ez := NewEventizer(pipeline)

	// Emit a realistic turn sequence
	ez.Emit(&event.Event{Kind: event.TurnStarted})
	ez.Emit(&event.Event{Kind: event.Reasoning, Text: "let me think"})
	ez.Emit(&event.Event{Kind: event.Text, Text: "Hello"})
	ez.Emit(&event.Event{
		Kind: event.Message, Text: "Hello world", Reasoning: "let me think",
		Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	})
	ez.Emit(&event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
		ID: "c1", Name: "read_file", Args: `{"path":"x"}`, ReadOnly: true,
	}})
	ez.Emit(&event.Event{Kind: event.ToolResult, Tool: event.Tool{
		ID: "c1", Name: "read_file", Output: "content",
	}})
	ez.Emit(&event.Event{Kind: event.TurnDone})
	logSink.Flush()

	// Collector should have all typed events
	if len(typed) < 7 {
		t.Fatalf("expected >=7 typed events, got %d", len(typed))
	}

	// Log replay matches
	loaded, err := ReadLog(logSink.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(typed) {
		t.Errorf("log replay: expected %d events, got %d", len(typed), len(loaded))
	}
}

func TestLegacySink(t *testing.T) {
	var got []Event
	pipeline := FuncSink(func(ev Event) { got = append(got, ev) })
	legacy := NewLegacySink(pipeline)

	legacy.Emit(&event.Event{Kind: event.Text, Text: "via legacy"})
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type() != "model.delta" {
		t.Errorf("type = %q", got[0].Type())
	}
}
