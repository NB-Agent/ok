package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	cu "github.com/NB-Agent/ok/internal/agent/computeruse"
	"github.com/NB-Agent/ok/internal/tool"
)

// computerUse wraps the ComputerUse orchestrator as a built-in tool.
// It is NOT registered via init() because it needs provider configuration at
// assembly time — boot.go wires it up just like the task tool.
type computerUse struct {
	cu *cu.ComputerUse
}

// NewComputerUseTool creates a Tool that runs the screenshot→analyze→act→verify loop.
func NewComputerUseTool(baseURL, apiKey, model string) tool.Tool {
	return &computerUse{
		cu: cu.NewComputerUse(apiKey, baseURL, model),
	}
}

func (c *computerUse) Name() string { return "computer-use" }

func (c *computerUse) Description() string {
	return "Control the computer visually. Takes a screenshot, analyzes it, then clicks/types/scrolls to achieve the goal. Use for tasks like 'open WeChat and send a message' or 'find the file and drag it to the folder'."
}

func (c *computerUse) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"goal":{"type":"string"}},"required":["goal"],"type":"object"}`)
}

func (c *computerUse) ReadOnly() bool { return false }

func (c *computerUse) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Goal string `json:"goal"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Goal == "" {
		return "", fmt.Errorf("goal is required")
	}

	result, err := c.cu.RunGoal(ctx, p.Goal)
	if err != nil {
		return "", err
	}

	return result, nil
}
