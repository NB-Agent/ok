package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(todoWrite{}) }

// todoWrite records the agent's running task list. It has no host side effects —
// the full list lives in the call's args (the model re-sends it whole on every
// update), which a frontend renders as a checklist. Execute just validates the
// shape and acks with a count, so the model gets a stable confirmation. The agent
// keeps one item in_progress at a time and flips each to completed as it finishes.
type todoWrite struct{}

type todoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm,omitempty"`
}

func (todoWrite) Name() string { return "todo_write" }

func (todoWrite) Description() string {
	return "Track a structured task list. One in_progress at a time; mark completed with evidence."
}

func (todoWrite) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"todos":{"items":{"properties":{"activeForm":{"type":"string"},"content":{"type":"string"},"status":{"enum":["pending","in_progress","completed"],"type":"string"}},"required":["content","status"],"type":"object"},"type":"array"}},"required":["todos"],"type":"object"}`)
}

// ReadOnly is true: todo_write only records a list (no filesystem or process
// effect), so it never needs approval and stays available in plan mode — where
// laying out a plan as todos is exactly the point.
func (todoWrite) ReadOnly() bool { return true }

func (todoWrite) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Todos []todoItem `json:"todos"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	// Silently skip items with empty content — models sometimes send incomplete
	// data, and erroring here would trigger a retry loop producing more rubbish.
	cleaned := p.Todos[:0]
	var skipped int
	for _, t := range p.Todos {
		if t.Content == "" {
			skipped++
			continue
		}
		cleaned = append(cleaned, t)
	}
	p.Todos = cleaned

	var done, active, pending int
	for i, t := range p.Todos {
		switch t.Status {
		case "completed":
			done++
		case "in_progress":
			active++
		case "pending", "":
			pending++
		default:
			return "", fmt.Errorf("todo %d: invalid status %q (want pending|in_progress|completed)", i+1, t.Status)
		}
	}
	if active > 1 {
		return "", fmt.Errorf("at most one todo item may be in_progress at a time — found %d (set others to pending or completed)", active)
	}
	msg := fmt.Sprintf("Todos updated: %d total — %d completed, %d in progress, %d pending.",
		len(p.Todos), done, active, pending)
	if skipped > 0 {
		msg += fmt.Sprintf(" (%d empty item(s) skipped)", skipped)
	}
	return msg, nil
}
