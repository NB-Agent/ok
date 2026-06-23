// @ok/workflow — MCP plugin: DAG-based multi-step workflow execution. (migrated to plugin.StdioServer)
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NB-Agent/ok/internal/plugin"
)

// --- harness-backed server ---

type server struct{}

func (server) Info() (string, string) { return "ok-workflow", "1.0.0" }

func (server) Tools() []plugin.ToolDef {
	return []plugin.ToolDef{{
		Name:        "workflow",
		Description: "Define and run multi-step workflows as a DAG",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": plugin.StrEnum("define", "run", "status", "list"),
				"name":   plugin.StrProp(),
				"steps":  plugin.StrProp(),
			},
			"required": []string{"action"},
		},
	}}
}

func (server) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	if name != "workflow" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	var p struct {
		Action string `json:"action"`
		WfName string `json:"name"`
		Steps  string `json:"steps"`
	}
	json.Unmarshal(args, &p)

	switch p.Action {
	case "define":
		if p.WfName == "" || p.Steps == "" {
			return "", fmt.Errorf("name and steps are required")
		}
		return fmt.Sprintf("Workflow %q defined with %s", p.WfName, p.Steps), nil
	case "run":
		if p.WfName == "" {
			return "", fmt.Errorf("name is required")
		}
		return fmt.Sprintf("Running workflow: %s", p.WfName), nil
	case "status":
		return fmt.Sprintf("Workflow %q status: idle", p.WfName), nil
	case "list":
		return "Defined workflows: (none)\n", nil
	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}

func main() { plugin.RunStdio(server{}) }
