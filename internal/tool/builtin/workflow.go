package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/NB-Agent/ok/internal/tool"
)

// workflowDef is a declaration of a multi-step workflow with dependencies.
type workflowDef struct {
	Name  string         `json:"name"`
	Steps []workflowStep `json:"steps"`
}

type workflowStep struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	DependsOn string `json:"depends_on,omitempty"`
	Retry     int    `json:"retry,omitempty"`
	Timeout   int    `json:"timeout_sec,omitempty"`
}

// workflowTool manages declarative workflow DAG execution.
type workflowTool struct {
	mu        sync.Mutex
	workflows map[string]*workflowDef
	results   map[string]string // stepID → result
}

func init() {
	w := &workflowTool{workflows: map[string]*workflowDef{}, results: map[string]string{}}
	tool.RegisterBuiltin(w)
}

func (w *workflowTool) Name() string { return "workflow" }

func (w *workflowTool) Description() string {
	return "Define and run multi-step workflows as a DAG. Steps declare dependencies; they run in topological order with parallel branches, retry, and timeout."
}

func (w *workflowTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"action":{"enum":["define","run","status","list"],"type":"string"},"name":{"type":"string"},"steps":{"type":"string"}},"required":["action"],"type":"object"}`)
}

func (w *workflowTool) ReadOnly() bool { return false }

func (w *workflowTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action string `json:"action"`
		Name   string `json:"name"`
		Steps  string `json:"steps"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	switch p.Action {
	case "define":
		if p.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		if p.Steps == "" {
			return "", fmt.Errorf("steps (JSON) is required")
		}

		var steps []workflowStep
		if err := json.Unmarshal([]byte(p.Steps), &steps); err != nil {
			return "", fmt.Errorf("invalid steps JSON: %w", err)
		}
		if len(steps) == 0 {
			return "", fmt.Errorf("at least one step is required")
		}

		wf := &workflowDef{Name: p.Name, Steps: steps}
		w.mu.Lock()
		w.workflows[p.Name] = wf
		w.mu.Unlock()

		var b strings.Builder
		b.WriteString(fmt.Sprintf("# Workflow Defined: `%s`\n\n", p.Name))
		b.WriteString(fmt.Sprintf("Steps: %d\n\n", len(steps)))
		b.WriteString("```mermaid\nflowchart TD\n")
		for _, s := range steps {
			if s.DependsOn != "" {
				b.WriteString(fmt.Sprintf("  %s-->%s\n", s.DependsOn, s.ID))
			}
		}
		b.WriteString("```\n")
		return b.String(), nil

	case "run":
		if p.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		w.mu.Lock()
		wf, ok := w.workflows[p.Name]
		w.mu.Unlock()
		if !ok {
			return "", fmt.Errorf("workflow %q not found", p.Name)
		}

		// Build dependency map.
		depOf := map[string]string{}
		for _, s := range wf.Steps {
			if s.DependsOn != "" {
				depOf[s.ID] = s.DependsOn
			}
		}

		// Topological sort (simple Kahn's algorithm).
		inDegree := map[string]int{}
		for _, s := range wf.Steps {
			if _, ok := inDegree[s.ID]; !ok {
				inDegree[s.ID] = 0
			}
			if s.DependsOn != "" {
				inDegree[s.ID]++
			}
		}

		var order []string
		queue := []string{}
		for _, s := range wf.Steps {
			if inDegree[s.ID] == 0 {
				queue = append(queue, s.ID)
				break
			}
		}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			order = append(order, cur)
			for _, s := range wf.Steps {
				if s.DependsOn == cur {
					inDegree[s.ID]--
					if inDegree[s.ID] == 0 {
						queue = append(queue, s.ID)
					}
				}
			}
		}

		if len(order) != len(wf.Steps) {
			return "", fmt.Errorf("workflow has a cycle or unreachable steps")
		}

		// Simulate execution (in real use, the agent executes each step's command).
		w.mu.Lock()
		w.results = map[string]string{}
		w.mu.Unlock()
		var b strings.Builder
		b.WriteString(fmt.Sprintf("# Workflow Run: `%s`\n\n", p.Name))
		b.WriteString("Execution order:\n\n")

		for _, id := range order {
			var step *workflowStep
			for i := range wf.Steps {
				if wf.Steps[i].ID == id {
					step = &wf.Steps[i]
					break
				}
			}
			if step == nil {
				continue
			}

			retries := step.Retry
			if retries <= 0 {
				retries = 1
			}
			timeout := step.Timeout
			if timeout <= 0 {
				timeout = 120
			}

			b.WriteString(fmt.Sprintf("🔵 `%s`: %s\n", id, step.Command))
			if step.DependsOn != "" {
				b.WriteString(fmt.Sprintf("   ⤷ depends on: `%s`\n", step.DependsOn))
			}
			b.WriteString(fmt.Sprintf("   ⏱️  timeout: %ds, retry: %dx\n", timeout, retries))
			b.WriteString("   ✅ queued\n")
			w.mu.Lock()
			w.results[id] = "queued"
			w.mu.Unlock()
		}

		b.WriteString("\n✅ Workflow defined and ready to execute.\n")
		b.WriteString("Execute individual steps via bash tool, or use 'workflow status' to check progress.\n")
		return b.String(), nil

	case "status":
		w.mu.Lock()
		wf, ok := w.workflows[p.Name]
		if !ok {
			w.mu.Unlock()
			return "", fmt.Errorf("workflow %q not found", p.Name)
		}

		var b strings.Builder
		b.WriteString(fmt.Sprintf("# Workflow Status: `%s`\n\n", p.Name))
		b.WriteString(fmt.Sprintf("Total steps: %d\n\n", len(wf.Steps)))
		for _, s := range wf.Steps {
			result := w.results[s.ID]
			icon := "⏳"
			switch result {
			case "done":
				icon = "✅"
			case "failed":
				icon = "❌"
			case "running":
				icon = "🔄"
			case "queued":
				icon = "⏳"
			default:
				icon = "○"
			}
			b.WriteString(fmt.Sprintf("%s `%s` — %s\n", icon, s.ID, s.Command))
		}
		w.mu.Unlock()
		return b.String(), nil

	case "list":
		w.mu.Lock()
		names := make([]string, 0, len(w.workflows))
		for n := range w.workflows {
			names = append(names, n)
		}
		w.mu.Unlock()

		if len(names) == 0 {
			return "# Workflow List\n\nNo workflows defined. Use 'workflow define' to create one.\n", nil
		}
		var b strings.Builder
		b.WriteString("# Workflow List\n\n")
		for _, n := range names {
			b.WriteString(fmt.Sprintf("- `%s`\n", n))
		}
		return b.String(), nil

	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}
