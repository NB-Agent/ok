package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
)

// planTool provides hierarchical task decomposition and execution.
// It builds a markdown plan from a goal and optional step list — the heavy
// planning engine lives in agent.Planner which is wired by the task tool.
type planTool struct{}

func init() { tool.RegisterBuiltin(planTool{}) }

func (planTool) Name() string { return "plan" }

func (planTool) Description() string {
	return "Break down complex goals into sub-tasks with dependencies. Execute step by step. Use for multi-step refactoring and architecture changes."
}

func (planTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"goal":{"type":"string"},"steps":{"type":"string"}},"required":["goal"],"type":"object"}`)
}

func (planTool) ReadOnly() bool { return false }

func (p planTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var req struct {
		Goal  string `json:"goal"`
		Steps string `json:"steps"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if req.Goal == "" {
		return "", fmt.Errorf("goal is required")
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Plan: %s\n\n", req.Goal))

	if req.Steps != "" {
		steps := splitArgs(req.Steps)
		b.WriteString(fmt.Sprintf("Tasks: %d total · ⏳ %d pending\n\n", len(steps), len(steps)))
		for i, s := range steps {
			status := "⏳ pending"
			if i == 0 {
				status = "🔄 running"
			}
			b.WriteString(fmt.Sprintf("- [ ] step-%d: %s (%s)\n", i+1, s, status))
		}
	} else {
		b.WriteString("Tasks: 5 total · ⏳ 5 pending\n\n")
		b.WriteString("- [ ] phase-1: Analyze the current state and gather requirements (🔄 running)\n")
		b.WriteString("- [ ] phase-2: Design the solution architecture (⏳ pending) ← phase-1\n")
		b.WriteString("- [ ] phase-3: Implement the changes (⏳ pending) ← phase-2\n")
		b.WriteString("- [ ] phase-4: Verify and fix any issues (⏳ pending) ← phase-3\n")
		b.WriteString("- [ ] phase-5: Final review and summary (⏳ pending) ← phase-4\n")
	}
	return b.String(), nil
}
