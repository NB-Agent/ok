package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/event"
)

func TestAskToolHeadlessFallback(t *testing.T) {
	tool := NewAskTool()
	// No asker in context → headless path triggered.
	ctx := context.Background()
	args := json.RawMessage(`{"questions":[{"header":"Lib","question":"Which lib?","options":[{"label":"A"},{"label":"B"}]}]}`)
	out, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("headless ask should not error: %v", err)
	}
	if !strings.Contains(out, "No interactive user") {
		t.Errorf("headless ask should return fallback message, got %q", out)
	}
}

func TestAskToolRejectsNoQuestions(t *testing.T) {
	tool := NewAskTool()
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"questions":[]}`))
	if err == nil {
		t.Fatal("expected error for empty questions array")
	}
	if !strings.Contains(err.Error(), "at least one question") {
		t.Errorf("error should say 'at least one question', got %v", err)
	}
}

func TestAskToolRejectsMissingQuestionText(t *testing.T) {
	tool := NewAskTool()
	// Missing "question" field — question text will be empty.
	_, err := tool.Execute(context.Background(), json.RawMessage(
		`{"questions":[{"header":"H","options":[{"label":"A"},{"label":"B"}]}]}`,
	))
	if err == nil {
		t.Fatal("expected error for missing question text")
	}
}

func TestAskToolRejectsTooFewOptions(t *testing.T) {
	tool := NewAskTool()
	_, err := tool.Execute(context.Background(), json.RawMessage(
		`{"questions":[{"header":"H","question":"Q?","options":[{"label":"A"}]}]}`,
	))
	if err == nil {
		t.Fatal("expected error for < 2 options")
	}
}

func TestAskToolRejectsEmptyOptionLabel(t *testing.T) {
	tool := NewAskTool()
	_, err := tool.Execute(context.Background(), json.RawMessage(
		`{"questions":[{"header":"H","question":"Q?","options":[{"label":""},{"label":"B"}]}]}`,
	))
	if err == nil {
		t.Fatal("expected error for empty option label")
	}
}

func TestAskToolInvalidJSONReturnsError(t *testing.T) {
	tool := NewAskTool()
	_, err := tool.Execute(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON args")
	}
}

func TestAskToolHeadlessWithAskerButNoAnswers(t *testing.T) {
	tool := NewAskTool()
	asker := &stubAsker{answers: nil} // returns empty answers (simulates ACP)
	ctx := withCallContext(context.Background(), "call-1", event.Discard, asker)
	args := json.RawMessage(`{"questions":[{"header":"Lib","question":"Which lib?","options":[{"label":"A"},{"label":"B"}]}]}`)
	out, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("should not error with empty answers: %v", err)
	}
	if !strings.Contains(out, "No interactive user") {
		t.Errorf("empty answers should trigger fallback, got %q", out)
	}
}

func TestAskToolWithAskerReturnsFormattedAnswers(t *testing.T) {
	tool := NewAskTool()
	asker := &stubAsker{
		answers: []event.AskAnswer{
			{QuestionID: "q1", Selected: []string{"option-a"}},
		},
	}
	ctx := withCallContext(context.Background(), "call-1", event.Discard, asker)
	args := json.RawMessage(`{"questions":[{"header":"Pick one","question":"Which?","options":[{"label":"option-a"},{"label":"option-b"}]}]}`)
	out, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}
	if !strings.Contains(out, "The user answered") {
		t.Errorf("output should contain answer summary, got %q", out)
	}
	if !strings.Contains(out, "option-a") {
		t.Errorf("output should contain the selected option, got %q", out)
	}
}

func TestFormatAnswers(t *testing.T) {
	qs := []event.AskQuestion{
		{ID: "q1", Header: "First", Prompt: "Q1?"},
		{ID: "q2", Header: "", Prompt: "Q2?"},
	}
	answers := []event.AskAnswer{
		{QuestionID: "q1", Selected: []string{"a", "b"}},
	}
	out := formatAnswers(qs, answers)
	if !strings.Contains(out, "First: a, b") {
		t.Errorf("should show header for q1, got %q", out)
	}
	if !strings.Contains(out, "(no answer)") {
		t.Errorf("should show (no answer) for q2, got %q", out)
	}
}

// stubAsker implements Asker for testing.
type stubAsker struct {
	answers []event.AskAnswer
	err     error
}

func (s *stubAsker) Ask(ctx context.Context, questions []event.AskQuestion) ([]event.AskAnswer, error) {
	return s.answers, s.err
}
