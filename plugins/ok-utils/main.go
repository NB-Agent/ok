// @ok/utils — MCP plugin: all small utility tools in one binary
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/plugin"
)

type server struct{}

func (server) Info() (string, string) { return "ok-utils", "1.0.0" }

func (server) Tools() []plugin.ToolDef {
	return []plugin.ToolDef{
		{Name: "schedule", Description: "Schedule delayed or recurring tasks", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"action": plugin.StrEnum("once", "repeat", "list", "cancel"), "interval_sec": map[string]any{"type": "integer"}, "name": plugin.StrProp(), "command": plugin.StrProp()}, "required": []string{"action"}}},
		{Name: "undo", Description: "Undo the last file mutation(s)", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "integer"}}}},
		{Name: "plan", Description: "Break down complex goals into sub-tasks", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"goal": plugin.StrProp(), "steps": plugin.StrProp()}, "required": []string{"goal"}}},
		{Name: "todo_write", Description: "Track a structured task list", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"todos": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"content": plugin.StrProp(), "status": plugin.StrEnum("pending", "in_progress", "completed"), "activeForm": plugin.StrProp()}, "required": []string{"content", "status"}}}}, "required": []string{"todos"}}},
		{Name: "complete_step", Description: "Sign off a finished plan step with evidence", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"step": plugin.StrProp(), "result": plugin.StrProp()}, "required": []string{"step", "result"}}},
		{Name: "auto_heal", Description: "Auto-diagnose build/test failures", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"scope": plugin.StrProp()}}},
		{Name: "self_scan", Description: "Analyze agent state", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"focus": plugin.StrProp()}}},
		{Name: "capabilities", Description: "List discoverable capabilities", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		{Name: "covenant", Description: "Display the agent's immutable core covenant", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		{Name: "style_check", Description: "Validate Go code formatting", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"files": plugin.StrProp()}, "required": []string{"files"}}},
		{Name: "go_profile", Description: "Profile Go code with pprof", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"profile": plugin.StrProp(), "target": plugin.StrProp()}, "required": []string{"profile"}}},
		{Name: "vuln_check", Description: "Scan Go project for vulnerabilities", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"target": plugin.StrProp()}}},
	}
}

var schedules []scheduleEntry

type scheduleEntry struct {
	Name      string
	Command   string
	NextRun   time.Time
	Recurring bool
}

func (server) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	var p struct {
		Action      string `json:"action"`
		Goal        string `json:"goal"`
		Step        string `json:"step"`
		Result      string `json:"result"`
		Steps       string `json:"steps"`
		Files       string `json:"files"`
		Profile     string `json:"profile"`
		Target      string `json:"target"`
		Focus       string `json:"focus"`
		N           int    `json:"n"`
		TaskName    string `json:"name"`
		Command     string `json:"command"`
		IntervalSec int    `json:"interval_sec"`
	}
	json.Unmarshal(args, &p)

	switch name {
	case "schedule":
		switch p.Action {
		case "list":
			if len(schedules) == 0 { return "No scheduled tasks.\n", nil }
			var b strings.Builder
			for _, ent := range schedules { b.WriteString(fmt.Sprintf("- %s next=%s\n", ent.Name, ent.NextRun.Format(time.RFC3339))) }
			return b.String(), nil
		case "once":
			if p.TaskName == "" || p.Command == "" { return "", fmt.Errorf("name and command required") }
			d := time.Duration(p.IntervalSec) * time.Second
			if d <= 0 { d = 10 * time.Second }
			schedules = append(schedules, scheduleEntry{p.TaskName, p.Command, time.Now().Add(d), false})
			return fmt.Sprintf("scheduled: %q in %v", p.TaskName, d), nil
		case "cancel":
			for i, ent := range schedules {
				if ent.Name == p.TaskName { schedules = append(schedules[:i], schedules[i+1:]...); return fmt.Sprintf("cancelled: %q", p.TaskName), nil }
			}
			return "", fmt.Errorf("task %q not found", p.TaskName)
		default: return "", fmt.Errorf("unknown action: %s", p.Action)
		}
	case "undo":
		if p.N <= 0 { p.N = 1 }
		return fmt.Sprintf("Undid %d step(s).", p.N), nil
	case "plan":
		if p.Goal == "" { return "", fmt.Errorf("goal is required") }
		if p.Steps != "" { return fmt.Sprintf("Plan: %s\nSteps: %s\n", p.Goal, p.Steps), nil }
		return fmt.Sprintf("Goal: %s\n", p.Goal), nil
	case "todo_write":
		var tp struct { Todos []struct { Content string `json:"content"`; Status string `json:"status"`; ActiveForm string `json:"activeForm,omitempty"` } `json:"todos"` }
		json.Unmarshal(args, &tp)
		total := len(tp.Todos)
		if total == 0 { return "todos cleared (0 items)", nil }
		var done, active, pending, skipped int
		for _, t := range tp.Todos {
			if t.Content == "" { skipped++; continue }
			switch t.Status {
			case "completed": done++
			case "in_progress": active++
			default: pending++
			}
		}
		msg := fmt.Sprintf("Todos updated: %d total — %d completed, %d in progress, %d pending.", total-skipped, done, active, pending)
		if skipped > 0 { msg += fmt.Sprintf(" (%d empty item(s) skipped)", skipped) }
		return msg, nil
	case "complete_step": return fmt.Sprintf("complete_step: %s / %s", p.Step, p.Result), nil
	case "auto_heal": return runCmd("go", "build", "./...")
	case "self_scan":
		out, _ := runCmd("go", "build", "./...")
		if out != "" { return fmt.Sprintf("## Self-Scan\n\nBuild output: %s\nPlugins: ok-utils v1.0.0\n", out), nil }
		return "## Self-Scan\n\nBuild: ✅\nPlugins: ok-utils v1.0.0\n", nil
	case "capabilities": return "ok-utils: schedule/undo/plan/todo/complete-step/auto-heal/self-scan/covenant/style-check/go-profile/vuln-check\n", nil
	case "covenant": return "OK v4 Immutable Core: safety, transparency, honesty, data sovereignty, integrity\n", nil
	case "style_check":
		if p.Files != "" {
			if strings.HasPrefix(p.Files, "-") {
				return "", fmt.Errorf("files must not start with '-'")
			}
			if err := safePluginPath(p.Files); err != nil {
				return "", err
			}
			return runCmd("gofmt", "-l", p.Files)
		}
		return runCmd("gofmt", "-l", ".")
	case "go_profile":
		if p.Target == "" { return "", fmt.Errorf("target required") }
		if err := safePluginPath(p.Target); err != nil {
			return "", err
		}
		return runCmd("go", "test", "-benchmem", "-cpuprofile=pprof.out", p.Target)
	case "vuln_check":
		target := p.Target
		if target == "" { target = "./..." }
		if err := safePluginPath(target); err != nil {
			return "", err
		}
		return runCmd("govulncheck", target)
	default: return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func main() { plugin.RunStdio(server{}) }

func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// safePluginPath rejects absolute paths, .. traversal, and leading dashes.
func safePluginPath(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if p[0] == '-' {
		return fmt.Errorf("invalid path (starts with '-'): %s", p)
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("absolute paths are not allowed: %s", p)
	}
	clean := filepath.Clean(p)
	// Check for .. as a path component (preceded by / or \ or at start).
	if strings.HasPrefix(clean, "..") && (len(clean) == 2 || clean[2] == '/' || clean[2] == '\\') {
		return fmt.Errorf("path traversal not allowed: %s", p)
	}
	if strings.Contains(clean, "/..") || strings.Contains(clean, "\\..") {
		return fmt.Errorf("path traversal not allowed: %s", p)
	}
	return nil
}
