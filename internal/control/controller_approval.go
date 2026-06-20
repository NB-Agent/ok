package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/event"
)

// Cancel ends the current turn (if any) without changing state.
func (c *Controller) Cancel() {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
}

// Approve answers an outstanding approval prompt. id is the Approval.ID from the
// event; session=true persists the grant for this session. The reply is
// consumed by the turn that issued the request; unknown ids are silently ignored.
func (c *Controller) Approve(id string, allow, session bool) {
	c.approval.Approve(id, allow, session)
}

// Answer replies to an outstanding ask prompt.
func (c *Controller) Answer(id string, answers []event.AskAnswer) {
	c.approval.Answer(id, answers)
}

// AnswerQuestion is an alias for Answer; used by some frontends.
func (c *Controller) AnswerQuestion(id string, answers []event.AskAnswer) {
	c.approval.Answer(id, answers)
}

// Ask implements agent.Asker. It emits an Ask event and blocks for the answer.
func (c *Controller) Ask(ctx context.Context, questions []event.AskQuestion) ([]event.AskAnswer, error) {
	return c.approval.Ask(ctx, questions)
}

// SetPlanMode toggles plan mode on the controller and the underlying executor.
func (c *Controller) SetPlanMode(v bool) {
	c.mu.Lock()
	c.planMode = v
	c.mu.Unlock()
	if c.executor != nil {
		c.executor.SetPlanMode(v)
	}
}

// PlanMode reports whether plan mode is on.
func (c *Controller) PlanMode() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.planMode
}

// --- plan seeding ---

// seedTodo is the plan-todo item shape used by seedPlanTodos.
type seedTodo struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

// seedPlanTodos turns an approved plan into a starter task list and emits it as a
// synthetic todo_write event, so the live task panel populates the instant the
// user approves — a structural guarantee, not a prompt the model might ignore.
func (c *Controller) seedPlanTodos(plan string) {
	args, err := PlanTodosJSON(plan)
	if err != nil {
		c.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: "failed to seed plan tasks: " + err.Error()})
		return
	}
	if args == "" {
		return
	}
	t := event.Tool{ID: "plan-seed", Name: "todo_write", Args: args, ReadOnly: true}
	c.sink.Emit(&event.Event{Kind: event.ToolDispatch, Tool: t})
	t.Output = "task list seeded from the approved plan"
	c.sink.Emit(&event.Event{Kind: event.ToolResult, Tool: t})
}

// --- plan parsing ---

// PlanTodosJSON parses an approved plan's markdown into todo_write-shaped args
// JSON ({"todos":[...]}), or ("", nil) when the plan has no list items.
func PlanTodosJSON(plan string) (string, error) {
	items := parsePlanTodos(plan)
	if len(items) == 0 {
		return "", nil
	}
	b, err := json.Marshal(map[string]any{"todos": items})
	if err != nil {
		return "", fmt.Errorf("marshal plan todos: %w", err)
	}
	return string(b), nil
}

// parsePlanTodos extracts a starter task list from an approved plan's markdown
// list items (bulleted or numbered): the first is in_progress, the rest pending,
// capped so a long plan can't flood the panel.
func parsePlanTodos(plan string) []seedTodo {
	todos := make([]seedTodo, 0, 8)
	for _, raw := range strings.Split(plan, "\n") {
		item := listItemContent(raw)
		if item == "" {
			continue
		}
		status := "pending"
		if len(todos) == 0 {
			status = "in_progress"
		}
		todos = append(todos, seedTodo{Content: item, Status: status})
		if len(todos) >= 20 {
			break
		}
	}
	return todos
}

// listItemContent returns the task text of a markdown list line ("- x", "* x",
// "1. x", "2) x"), or "" if the line isn't a list item. Light inline-markdown
// stripping keeps the checklist readable.
func listItemContent(line string) string {
	s := strings.TrimSpace(line)
	if s == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(s, "- "), strings.HasPrefix(s, "* "), strings.HasPrefix(s, "+ "):
		s = s[2:]
	default:
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 || i+1 >= len(s) || (s[i] != '.' && s[i] != ')') || s[i+1] != ' ' {
			return ""
		}
		s = s[i+2:]
	}
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[ ] ")
	s = strings.TrimPrefix(s, "[x] ")
	s = strings.TrimPrefix(s, "[X] ")
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "`", "")
	return strings.TrimSpace(s)
}
