package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/tool"
)

// planTool provides hierarchical task decomposition and execution.
type planTool struct {
	planner *agent.Planner
}

func init() { tool.RegisterBuiltin(planTool{planner: agent.NewPlanner()}) }

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

	plan := agent.NewPlan(req.Goal)

	if req.Steps != "" {
		// Explicit steps mode: use user-provided steps.
		steps := splitArgs(req.Steps)
		for i, s := range steps {
			id := fmt.Sprintf("step-%d", i+1)
			if i == 0 {
				plan.AddTask(id, s)
			} else {
				plan.AddTask(id, s, fmt.Sprintf("step-%d", i))
			}
		}
	} else {
		// Auto-decompose into phases.
		plan.AddTask("phase-1", "Analyze the current state and gather requirements")
		plan.AddTask("phase-2", "Design the solution architecture",
			"phase-1")
		plan.AddTask("phase-3", "Implement the changes",
			"phase-2")
		plan.AddTask("phase-4", "Verify and fix any issues",
			"phase-3")
		plan.AddTask("phase-5", "Final review and summary",
			"phase-4")
	}

	return plan.Summary(), nil
}
