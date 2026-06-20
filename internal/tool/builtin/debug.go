package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/winhide"
)

func init() { tool.RegisterBuiltin(debugTool{}) }

// debugTool integrates with Delve (dlv) for Go debugging.
// Actions: start, break, continue, next, step, print, stack, locals, list, restart, stop.
type debugTool struct{}

func (debugTool) Name() string { return "debug" }

func (debugTool) Description() string {
	return "Debug Go programs using Delve (dlv). Set breakpoints, step through code, inspect variables, view stack traces. Requires 'dlv' in PATH."
}

func (debugTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"action":{"enum":["start","break","continue","next","step","print","stack","locals","list","restart","stop","set"],"type":"string"},"args":{"type":"string"},"target":{"type":"string"}},"required":["action"],"type":"object"}`)
}

func (debugTool) ReadOnly() bool { return false }

func (debugTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action string `json:"action"`
		Target string `json:"target"`
		Args   string `json:"args"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	switch p.Action {
	case "start":
		if p.Target == "" {
			return "", fmt.Errorf("target (package path) is required")
		}
		dlvArgs := []string{"debug", "--headless", "--listen=:2345", "--api-version=2"}
		if p.Args != "" {
			dlvArgs = append(dlvArgs, "--", p.Args)
		}
		dlvArgs = append(dlvArgs, p.Target)
		cmd := winhide.CommandContext(ctx, "dlv", dlvArgs...)
		out, err := cmd.CombinedOutput()
		return fmt.Sprintf("# Debug Start\n\n```\n%s\n```\n%v\n", strings.TrimSpace(string(out)), err), nil

	case "break":
		if p.Target == "" {
			return "", fmt.Errorf("target (file:line or function) is required")
		}
		cmd := winhide.CommandContext(ctx, "dlv", "break", p.Target)
		out, _ := cmd.CombinedOutput()
		return fmt.Sprintf("# Debug Breakpoint\n\n`%s`\n```\n%s\n```\n", p.Target, strings.TrimSpace(string(out))), nil

	case "continue":
		cmd := winhide.CommandContext(ctx, "dlv", "continue")
		out, _ := cmd.CombinedOutput()
		return fmt.Sprintf("# Debug Continue\n\n```\n%s\n```\n", strings.TrimSpace(string(out))), nil

	case "next":
		cmd := winhide.CommandContext(ctx, "dlv", "next")
		out, _ := cmd.CombinedOutput()
		return fmt.Sprintf("# Debug Next\n\n```\n%s\n```\n", strings.TrimSpace(string(out))), nil

	case "step":
		cmd := winhide.CommandContext(ctx, "dlv", "step")
		out, _ := cmd.CombinedOutput()
		return fmt.Sprintf("# Debug Step\n\n```\n%s\n```\n", strings.TrimSpace(string(out))), nil

	case "print":
		if p.Target == "" {
			return "", fmt.Errorf("target (variable/expression) is required")
		}
		cmd := winhide.CommandContext(ctx, "dlv", "print", p.Target)
		out, _ := cmd.CombinedOutput()
		return fmt.Sprintf("# Debug Print\n\n`%s` = \n```\n%s\n```\n", p.Target, strings.TrimSpace(string(out))), nil

	case "stack":
		cmd := winhide.CommandContext(ctx, "dlv", "stack")
		out, _ := cmd.CombinedOutput()
		return fmt.Sprintf("# Debug Stack\n\n```\n%s\n```\n", strings.TrimSpace(string(out))), nil

	case "locals":
		cmd := winhide.CommandContext(ctx, "dlv", "locals")
		out, _ := cmd.CombinedOutput()
		return fmt.Sprintf("# Debug Locals\n\n```\n%s\n```\n", strings.TrimSpace(string(out))), nil

	case "list":
		if p.Target == "" {
			p.Target = "."
		}
		// list source around current PC or at file:line
		args := []string{"list"}
		if p.Target != "" {
			args = append(args, p.Target)
		}
		cmd := winhide.CommandContext(ctx, "dlv", args...)
		out, _ := cmd.CombinedOutput()
		return fmt.Sprintf("# Debug List\n\n```\n%s\n```\n", strings.TrimSpace(string(out))), nil

	case "restart":
		cmd := winhide.CommandContext(ctx, "dlv", "restart")
		out, _ := cmd.CombinedOutput()
		return fmt.Sprintf("# Debug Restart\n\n```\n%s\n```\n", strings.TrimSpace(string(out))), nil

	case "stop":
		cmd := winhide.CommandContext(ctx, "dlv", "quit")
		out, _ := cmd.CombinedOutput()
		return fmt.Sprintf("# Debug Stop\n\n```\n%s\n```\n", strings.TrimSpace(string(out))), nil

	case "set":
		if p.Target == "" {
			return "", fmt.Errorf("target (variable=value) is required")
		}
		cmd := winhide.CommandContext(ctx, "dlv", "set", p.Target)
		out, _ := cmd.CombinedOutput()
		return fmt.Sprintf("# Debug Set\n\n```\n%s\n```\n", strings.TrimSpace(string(out))), nil

	default:
		return "", fmt.Errorf("unknown debug action: %s", p.Action)
	}
}
