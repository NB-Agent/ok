package eventpipe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserMessageEvent(t *testing.T) {
	ev := NewUserMessageEvent(1, 0, "hello")
	if ev.Type() != "user.message" {
		t.Errorf("type = %q", ev.Type())
	}
	if ev.Text != "hello" {
		t.Errorf("text = %q", ev.Text)
	}
	if ev.Meta().Turn != 1 {
		t.Errorf("turn = %d", ev.Meta().Turn)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	original := NewToolIntentEvent(2, 1, "call-1", "read_file", `{"path":"x"}`, true, "")
	data, err := MarshalEvent(original)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := unmarshalEvent(data)
	if err != nil {
		t.Fatal(err)
	}
	back, ok := parsed.(ToolIntentEvent)
	if !ok {
		t.Fatalf("type = %T", parsed)
	}
	if back.CallID != "call-1" || back.Name != "read_file" || back.Args != `{"path":"x"}` {
		t.Errorf("round trip mismatch: %+v", back)
	}
	if !back.ReadOnly {
		t.Error("ReadOnly should be true")
	}
	if back.Meta().Turn != 2 {
		t.Errorf("turn = %d", back.Meta().Turn)
	}
}

func TestFilterSink(t *testing.T) {
	var got []Event
	inner := FuncSink(func(ev Event) { got = append(got, ev) })
	filtered := Filter(inner, ByType("user.message", "model.final"))

	filtered.Emit(NewUserMessageEvent(1, 0, "hi"))
	filtered.Emit(NewModelDeltaEvent(1, 1, "content", "hello"))
	filtered.Emit(NewModelFinalEvent(1, 2, "hello", "", Usage{}, 0))

	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].Type() != "user.message" || got[1].Type() != "model.final" {
		t.Errorf("filtered wrong types: %q %q", got[0].Type(), got[1].Type())
	}
}

func TestSkipType(t *testing.T) {
	var got []Event
	inner := FuncSink(func(ev Event) { got = append(got, ev) })
	filtered := Filter(inner, SkipType("model.delta"))

	filtered.Emit(NewUserMessageEvent(1, 0, "hi"))
	filtered.Emit(NewModelDeltaEvent(1, 1, "content", "delta"))
	filtered.Emit(NewToolIntentEvent(1, 2, "c1", "read_file", "{}", true, ""))

	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].Type() != "user.message" || got[1].Type() != "tool.intent" {
		t.Errorf("filtered wrong types: %q %q", got[0].Type(), got[1].Type())
	}
}

func TestFanOut(t *testing.T) {
	var a, b []Event
	fa := FuncSink(func(ev Event) { a = append(a, ev) })
	fb := FuncSink(func(ev Event) { b = append(b, ev) })
	fanned := FanOut(fa, fb)

	fanned.Emit(NewStatusEvent(1, 0, "working"))

	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("fanout: a=%d b=%d", len(a), len(b))
	}
	if a[0].Type() != "status" || b[0].Type() != "status" {
		t.Error("fanout wrong event type")
	}
}

func TestFanOutNil(t *testing.T) {
	s := FanOut(nil, nil)
	s.Emit(NewStatusEvent(1, 0, "x"))

	s = FanOut()
	s.Emit(NewStatusEvent(1, 0, "x"))

	s = FanOut(FuncSink(func(Event) {}))
	s.Emit(NewStatusEvent(1, 0, "x"))
}

func TestFanOutByType(t *testing.T) {
	var tools, other []Event
	routes := map[string]Sink{
		"tool.intent": FuncSink(func(ev Event) { tools = append(tools, ev) }),
		"tool.result": FuncSink(func(ev Event) { tools = append(tools, ev) }),
	}
	fallback := FuncSink(func(ev Event) { other = append(other, ev) })
	s := FanOutByType(routes, fallback)

	s.Emit(NewToolIntentEvent(1, 0, "c1", "read_file", "{}", true, ""))
	s.Emit(NewUserMessageEvent(1, 1, "hi"))

	if len(tools) != 1 {
		t.Errorf("expected 1 tool event, got %d", len(tools))
	}
	if len(other) != 1 {
		t.Errorf("expected 1 other event, got %d", len(other))
	}
}

func TestTap(t *testing.T) {
	var sideEffects []Event
	var main []Event

	tapped := Tap(FuncSink(func(ev Event) { main = append(main, ev) }),
		func(ev Event) { sideEffects = append(sideEffects, ev) })

	tapped.Emit(NewNoticeEvent(1, 0, "hello", NoticeLevelInfo))

	if len(sideEffects) != 1 || len(main) != 1 {
		t.Fatalf("tap: side=%d main=%d", len(sideEffects), len(main))
	}
}

func TestConversationReducer(t *testing.T) {
	v := EmptyConversation()

	v = ConversationReducer(v, NewUserMessageEvent(1, 0, "hello"))
	v = ConversationReducer(v, NewModelTurnStartedEvent(1, 1, "m1", "high", "abc"))
	v = ConversationReducer(v, NewModelDeltaEvent(1, 2, "reasoning", "thinking"))
	v = ConversationReducer(v, NewModelFinalEvent(1, 3, "Hello world", "thinking", Usage{PromptTokens: 10}, 0.001))
	v = ConversationReducer(v, NewToolIntentEvent(1, 4, "c1", "read_file", `{"path":"x"}`, true, ""))
	v = ConversationReducer(v, NewToolResultEvent(1, 5, "c1", "read_file", true, "file content", "", false, 100))

	if len(v.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(v.Messages))
	}
	if v.Messages[0].Role != "user" || v.Messages[0].Content != "hello" {
		t.Errorf("msg0 = %+v", v.Messages[0])
	}
	if v.Messages[1].Role != "assistant" || v.Messages[1].Content != "Hello world" {
		t.Errorf("msg1 = %+v", v.Messages[1])
	}
	if v.Messages[2].Role != "tool" || v.Messages[2].Content != "file content" {
		t.Errorf("msg2 = %+v", v.Messages[2])
	}
	if len(v.PendingToolCalls) != 0 {
		t.Errorf("expected no pending tools, got %d", len(v.PendingToolCalls))
	}
}

func TestConversationReducerWithDenied(t *testing.T) {
	v := EmptyConversation()
	v = ConversationReducer(v, NewToolIntentEvent(1, 0, "c1", "bash", `{"command":"rm -rf /"}`, false, ""))
	v = ConversationReducer(v, NewToolDeniedEvent(1, 1, "c1", "bash", "permission"))

	if len(v.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(v.Messages))
	}
	if v.Messages[0].Content != "denied: permission" {
		t.Errorf("content = %q", v.Messages[0].Content)
	}
	if len(v.PendingToolCalls) != 0 {
		t.Errorf("expected no pending tools, got %d", len(v.PendingToolCalls))
	}
}

func TestBudgetReducer(t *testing.T) {
	v := EmptyBudget(1.0)

	v = BudgetReducer(v, NewModelFinalEvent(1, 0, "a", "", Usage{PromptTokens: 100, CompletionTokens: 20}, 0.005))
	v = BudgetReducer(v, NewModelFinalEvent(2, 1, "b", "", Usage{PromptTokens: 50, CompletionTokens: 10}, 0.003))

	if v.SpentUSD < 0.007 || v.SpentUSD > 0.009 {
		t.Errorf("spentUSD = %f", v.SpentUSD)
	}
	if v.PromptTokens != 150 || v.CompletionTokens != 30 {
		t.Errorf("tokens = %d/%d", v.PromptTokens, v.CompletionTokens)
	}
	if v.Warned || v.Blocked {
		t.Error("should not be warned/blocked yet")
	}

	v = BudgetReducer(v, NewBudgetWarningEvent(3, 2, 0.5, 1.0))
	if !v.Warned {
		t.Error("should be warned")
	}

	v = BudgetReducer(v, NewBudgetBlockedEvent(4, 3, 1.0, 1.0))
	if !v.Blocked {
		t.Error("should be blocked")
	}
}

func TestPlanReducer(t *testing.T) {
	v := EmptyPlan()

	steps := []PlanStep{
		{ID: "s1", Title: "Read files", Action: "grep for patterns", Risk: "low"},
		{ID: "s2", Title: "Fix bugs", Action: "edit files", Risk: "med"},
	}
	v = PlanReducer(v, NewPlanSubmittedEvent(1, 0, steps, "I will fix the bugs"))

	if len(v.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(v.Steps))
	}
	if v.Steps[0].Completed || v.Steps[1].Completed {
		t.Error("steps should not be completed yet")
	}
	if v.SubmittedTurn != 1 {
		t.Errorf("submitted turn = %d", v.SubmittedTurn)
	}

	v = PlanReducer(v, NewPlanStepChangedEvent(2, 1, "s1", "Read files", "", "completed"))
	if !v.Steps[0].Completed {
		t.Error("step 1 should be completed")
	}
	if v.Steps[1].Completed {
		t.Error("step 2 should not be completed")
	}
}

func TestReplay(t *testing.T) {
	events := []Event{
		NewUserMessageEvent(1, 0, "hello"),
		NewModelTurnStartedEvent(1, 1, "m1", "high", "abc"),
		NewModelDeltaEvent(1, 2, "reasoning", "thinking"),
		NewModelDeltaEvent(1, 3, "content", "Hello"),
		NewModelFinalEvent(1, 4, "Hello", "thinking", Usage{PromptTokens: 10}, 0.001),
		NewToolIntentEvent(1, 5, "c1", "read_file", `{}`, true, ""),
		NewToolResultEvent(1, 6, "c1", "read_file", true, "content", "", false, 50),
		NewModelFinalEvent(2, 7, "Done", "", Usage{CompletionTokens: 5}, 0.002),
	}

	proj := Replay(events, 5.0)
	if len(proj.Conversation.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(proj.Conversation.Messages))
	}
	// Messages: [0]=user "hello", [1]=assistant "Hello", [2]=tool "content", [3]=assistant "Done"
	if proj.Conversation.Messages[1].Content != "Hello" {
		t.Errorf("msg1 = %q (want Hello)", proj.Conversation.Messages[1].Content)
	}
	if proj.Conversation.Messages[2].Content != "content" {
		t.Errorf("msg2 = %q (want content)", proj.Conversation.Messages[2].Content)
	}
	if proj.Conversation.Messages[3].Content != "Done" {
		t.Errorf("msg3 = %q (want Done)", proj.Conversation.Messages[3].Content)
	}
	if proj.Budget.SpentUSD < 0.0025 {
		t.Errorf("budget spent = %f", proj.Budget.SpentUSD)
	}
}

func TestLogSinkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewLogSink(LogConfig{Dir: dir, SessionID: "test-session"})
	if err != nil {
		t.Fatal(err)
	}

	events := []Event{
		NewUserMessageEvent(1, 0, "hello"),
		NewModelTurnStartedEvent(1, 1, "m1", "high", "abc123"),
		NewToolIntentEvent(1, 2, "c1", "read_file", `{"path":"x.go"}`, true, ""),
		NewToolResultEvent(1, 3, "c1", "read_file", true, "package main", "", false, 42),
		NewModelFinalEvent(1, 4, "Hello", "thinking", Usage{PromptTokens: 10, CompletionTokens: 5}, 0.001),
		NewTurnDoneEvent(1, 5, nil),
	}
	for _, ev := range events {
		sink.Emit(ev)
	}
	sink.Close()

	path := filepath.Join(dir, "test-session.events.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log file not created: %v", err)
	}

	loaded, err := ReadLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(events) {
		t.Fatalf("expected %d events, got %d", len(events), len(loaded))
	}
	for i, ev := range loaded {
		if ev.Type() != events[i].Type() {
			t.Errorf("event %d: type %q != %q", i, ev.Type(), events[i].Type())
		}
	}

	proj1 := Replay(events, 0)
	proj2 := Replay(loaded, 0)
	if len(proj1.Conversation.Messages) != len(proj2.Conversation.Messages) {
		t.Errorf("message count mismatch: %d vs %d", len(proj1.Conversation.Messages), len(proj2.Conversation.Messages))
	}
}

func TestLogSinkCorruptLine(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewLogSink(LogConfig{Dir: dir, SessionID: "corrupt-test"})
	if err != nil {
		t.Fatal(err)
	}

	sink.Emit(NewUserMessageEvent(1, 0, "hello"))
	sink.Close()

	path := filepath.Join(dir, "corrupt-test.events.jsonl")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString("{corrupt\n")
	f.WriteString("not json\n")
	f.Close()

	loaded, err := ReadLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 valid event, got %d", len(loaded))
	}
}

func TestMarshalUnmarshalAllTypes(t *testing.T) {
	types := map[string]Event{
		"user.message":       NewUserMessageEvent(1, 0, "hi"),
		"model.turn.started": NewModelTurnStartedEvent(1, 1, "m1", "high", "h123"),
		"model.delta":        NewModelDeltaEvent(1, 2, "content", "hello"),
		"model.final":        NewModelFinalEvent(1, 3, "hi", "", Usage{PromptTokens: 5}, 0.0),
		"tool.preparing":     NewToolPreparingEvent(1, 4, "c1", "read_file"),
		"tool.intent":        NewToolIntentEvent(1, 5, "c1", "read_file", `{}`, true, ""),
		"tool.denied":        NewToolDeniedEvent(1, 6, "c1", "bash", "permission"),
		"tool.result":        NewToolResultEvent(1, 7, "c1", "read_file", true, "out", "", false, 10),
		"status":             NewStatusEvent(1, 8, "working"),
		"error":              NewErrorEvent(1, 9, "boom", false),
		"notice":             NewNoticeEvent(1, 10, "note", NoticeLevelInfo),
		"phase":              NewPhaseEvent(1, 11, "planning"),
		"approval.request":   NewApprovalRequestEvent(1, 12, "a1", "bash", "rm -rf /"),
		"ask.request":        NewAskRequestEvent(1, 13, "aq1", nil),
		"turn.done":          NewTurnDoneEvent(1, 14, nil),
		"turn.aborted":       NewTurnAbortedEvent(1, 15, nil, "p2", "covenant violation"),
		"plan.submitted":     NewPlanSubmittedEvent(1, 16, nil, "my plan"),
		"plan.step.changed":  NewPlanStepChangedEvent(1, 17, "s1", "step 1", "done", "completed"),
		"budget.warning":     NewBudgetWarningEvent(1, 18, 0.5, 1.0),
		"budget.blocked":     NewBudgetBlockedEvent(1, 19, 1.0, 1.0),
		"session.opened":     NewSessionOpenedEvent(1, 20, "sess-1", 0),
		"session.compacted":  NewSessionCompactedEvent(1, 21, 10, 3, "user"),
	}

	for typeName, ev := range types {
		data, err := MarshalEvent(ev)
		if err != nil {
			t.Errorf("marshal %s: %v", typeName, err)
			continue
		}
		back, err := unmarshalEvent(data)
		if err != nil {
			t.Errorf("unmarshal %s: %v", typeName, err)
			continue
		}
		if back.Type() != typeName {
			t.Errorf("%s: type mismatch %q", typeName, back.Type())
		}
		if back.Meta().Turn != ev.Meta().Turn {
			t.Errorf("%s: turn %d != %d", typeName, back.Meta().Turn, ev.Meta().Turn)
		}
	}

	_, err := unmarshalEvent([]byte(`{"type":"unknown.type","meta":{"id":0,"ts":"","turn":0},"body":{}}`))
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestDiscard(t *testing.T) {
	Discard.Emit(NewUserMessageEvent(1, 0, "hi"))
	Discard.Emit(NewToolResultEvent(1, 1, "c1", "read_file", true, "", "", false, 0))
}

func TestFilterNil(t *testing.T) {
	s := Filter(nil, ByType("user.message"))
	s.Emit(NewUserMessageEvent(1, 0, "hi"))
}

func TestMapTransform(t *testing.T) {
	var got []Event
	mapped := Map(FuncSink(func(ev Event) { got = append(got, ev) }),
		func(ev Event) Event {
			if notice, ok := ev.(NoticeEvent); ok {
				return NewNoticeEvent(notice.Meta().Turn, notice.Meta().ID,
					strings.ToUpper(notice.Text), notice.Level)
			}
			return ev
		})

	mapped.Emit(NewNoticeEvent(1, 0, "hello", NoticeLevelInfo))
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	back, ok := got[0].(NoticeEvent)
	if !ok || back.Text != "HELLO" {
		t.Errorf("mapped text = %q", back.Text)
	}
}

func TestMapDrop(t *testing.T) {
	var got []Event
	mapped := Map(FuncSink(func(ev Event) { got = append(got, ev) }),
		func(ev Event) Event {
			if _, ok := ev.(ModelDeltaEvent); ok {
				return nil
			}
			return ev
		})

	mapped.Emit(NewModelDeltaEvent(1, 0, "content", "drop me"))
	mapped.Emit(NewUserMessageEvent(1, 1, "keep me"))

	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type() != "user.message" {
		t.Errorf("wrong type: %q", got[0].Type())
	}
}

func TestPipeChaining(t *testing.T) {
	var order []string
	a := FuncSink(func(ev Event) { order = append(order, "a:"+ev.Type()) })
	b := FuncSink(func(ev Event) { order = append(order, "b:"+ev.Type()) })
	c := FuncSink(func(ev Event) { order = append(order, "c:"+ev.Type()) })

	pipe := Pipe(a, b, c)
	pipe.Emit(NewStatusEvent(1, 0, "test"))

	if len(order) != 3 {
		t.Fatalf("expected 3 calls, got %d: %v", len(order), order)
	}
	if order[0] != "a:status" || order[1] != "b:status" || order[2] != "c:status" {
		t.Errorf("order = %v", order)
	}
}
