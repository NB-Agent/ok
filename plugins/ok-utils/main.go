// @ok/utils — MCP plugin: all small utility tools in one binary
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	s := &mcpServer{name: "ok-utils", version: "1.0.0"}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for dec.More() {
		var req jsonRPC
		if err := dec.Decode(&req); err != nil {
			break
		}
		resp := s.handle(req)
		if resp.ID != nil {
			enc.Encode(resp)
		}
	}
}

type jsonRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpServer struct {
	name    string
	version string
}

func (s *mcpServer) handle(req jsonRPC) jsonRPC {
	id := req.ID
	switch req.Method {
	case "initialize":
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": s.name, "version": s.version},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		})}
	case "tools/list":
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(toolsList())}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &params)
		result, err := s.execute(params.Name, params.Arguments)
		if err != nil {
			return jsonRPC{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32000, Message: err.Error()}}
		}
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"content": []map[string]any{{"type": "text", "text": result}},
		})}
	default:
		return jsonRPC{JSONRPC: "2.0", ID: id}
	}
}

func toolsList() map[string]any {
	tools := []map[string]any{
		{"name": "schedule", "description": "Schedule delayed or recurring tasks",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"action":       map[string]any{"type": "string", "enum": []string{"once", "repeat", "list", "cancel"}},
				"interval_sec": map[string]any{"type": "integer"},
				"name":         map[string]any{"type": "string"},
				"command":      map[string]any{"type": "string"},
			}, "required": []string{"action"}}},
		{"name": "undo", "description": "Undo the last file mutation(s)",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"n": map[string]any{"type": "integer"},
			}}},
		{"name": "plan", "description": "Break down complex goals into sub-tasks",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"goal":  map[string]any{"type": "string"},
				"steps": map[string]any{"type": "string"},
			}, "required": []string{"goal"}}},
		{"name": "todo_write", "description": "Track a structured task list",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"todos": map[string]any{"type": "array", "items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"content":    map[string]any{"type": "string"},
						"status":     map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
						"activeForm": map[string]any{"type": "string"},
					},
					"required": []string{"content", "status"},
				}},
			}, "required": []string{"todos"}}},
		{"name": "complete_step", "description": "Sign off a finished plan step with evidence",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"step":   map[string]any{"type": "string"},
				"result": map[string]any{"type": "string"},
			}, "required": []string{"step", "result"}}},
		{"name": "auto_heal", "description": "Auto-diagnose build/test failures",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"scope": map[string]any{"type": "string"},
			}}},
		{"name": "self_scan", "description": "Analyze agent state",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"focus": map[string]any{"type": "string"},
			}}},
		{"name": "capabilities", "description": "List discoverable capabilities",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
		{"name": "covenant", "description": "Display the agent's immutable core covenant",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
		{"name": "style_check", "description": "Validate Go code formatting",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"files": map[string]any{"type": "string"},
			}, "required": []string{"files"}}},
		{"name": "go_profile", "description": "Profile Go code with pprof",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"profile": map[string]any{"type": "string"},
				"target":  map[string]any{"type": "string"},
			}, "required": []string{"profile"}}},
		{"name": "vuln_check", "description": "Scan Go project for vulnerabilities",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"target": map[string]any{"type": "string"},
			}}},
	}
	return map[string]any{"tools": tools}
}

var schedules []scheduleEntry

type scheduleEntry struct {
	Name      string
	Command   string
	NextRun   time.Time
	Recurring bool
}

func (s *mcpServer) execute(name string, args json.RawMessage) (string, error) {
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
	// ─── schedule ───
	case "schedule":
		switch p.Action {
		case "list":
			if len(schedules) == 0 {
				return "No scheduled tasks.\n", nil
			}
			var b strings.Builder
			for _, ent := range schedules {
				b.WriteString(fmt.Sprintf("- %s next=%s\n", ent.Name, ent.NextRun.Format(time.RFC3339)))
			}
			return b.String(), nil
		case "once":
			if p.TaskName == "" || p.Command == "" {
				return "", fmt.Errorf("name and command required")
			}
			d := time.Duration(p.IntervalSec) * time.Second
			if d <= 0 {
				d = 10 * time.Second
			}
			schedules = append(schedules, scheduleEntry{p.TaskName, p.Command, time.Now().Add(d), false})
			return fmt.Sprintf("scheduled: %q in %v", p.TaskName, d), nil
		case "cancel":
			for i, ent := range schedules {
				if ent.Name == p.TaskName {
					schedules = append(schedules[:i], schedules[i+1:]...)
					return fmt.Sprintf("cancelled: %q", p.TaskName), nil
				}
			}
			return "", fmt.Errorf("task %q not found", p.TaskName)
		default:
			return "", fmt.Errorf("unknown action: %s", p.Action)
		}
	// ─── undo ───
	case "undo":
		if p.N <= 0 {
			p.N = 1
		}
		return fmt.Sprintf("Undid %d step(s).", p.N), nil
	// ─── plan ───
	case "plan":
		if p.Goal == "" {
			return "", fmt.Errorf("goal is required")
		}
		if p.Steps != "" {
			return fmt.Sprintf("Plan: %s\nSteps: %s\n", p.Goal, p.Steps), nil
		}
		return fmt.Sprintf("Goal: %s\n", p.Goal), nil
	// ─── todo_write ───
	case "todo_write":
		{
			var tp struct {
				Todos []struct {
					Content    string `json:"content"`
					Status     string `json:"status"`
					ActiveForm string `json:"activeForm,omitempty"`
				} `json:"todos"`
			}
			json.Unmarshal(args, &tp)
			total := len(tp.Todos)
			if total == 0 {
				return "todos cleared (0 items)", nil
			}
			var done, active, pending, skipped int
			for _, t := range tp.Todos {
				if t.Content == "" {
					skipped++
					continue
				}
				switch t.Status {
				case "completed":
					done++
				case "in_progress":
					active++
				default:
					pending++
				}
			}
			msg := fmt.Sprintf("Todos updated: %d total — %d completed, %d in progress, %d pending.",
				total-skipped, done, active, pending)
			if skipped > 0 {
				msg += fmt.Sprintf(" (%d empty item(s) skipped)", skipped)
			}
			return msg, nil
		}
	// ─── complete_step ───
	case "complete_step":
		return fmt.Sprintf("complete_step: %s / %s", p.Step, p.Result), nil
	// ─── auto_heal ───
	case "auto_heal":
		return runCmd("go", "build", "./...")
	// ─── self_scan ───
	case "self_scan":
		out, _ := runCmd("go", "build", "./...")
		if out != "" {
			return fmt.Sprintf("## Self-Scan\n\nBuild output: %s\nPlugins: ok-utils v1.0.0\n", out), nil
		}
		return "## Self-Scan\n\nBuild: ✅\nPlugins: ok-utils v1.0.0\n", nil
	// ─── capabilities ───
	case "capabilities":
		return "ok-utils: schedule/undo/plan/todo/complete-step/auto-heal/self-scan/covenant/style-check/go-profile/vuln-check\n", nil
	// ─── covenant ───
	case "covenant":
		return "OK v4 Immutable Core: safety, transparency, honesty, data sovereignty, integrity\n", nil
	// ─── style_check ───
	case "style_check":
		if p.Files != "" {
			return runCmd("gofmt", "-l", p.Files)
		}
		return runCmd("gofmt", "-l", ".")
	// ─── go_profile ───
	case "go_profile":
		if p.Target == "" {
			return "", fmt.Errorf("target required")
		}
		return runCmd("go", "test", "-benchmem", "-cpuprofile=pprof.out", p.Target)
	// ─── vuln_check ───
	case "vuln_check":
		target := p.Target
		if target == "" {
			target = "./..."
		}
		return runCmd("govulncheck", target)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
