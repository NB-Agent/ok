// Package agent provides the Reasoner — OK's top-level multi-agent orchestrator
// that decomposes a goal into a task DAG via LLM, then executes it with
// Planner's DAG engine using concurrent dispatch.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/reasoner"
)

// decomposePrompt steers the decomposition model toward a verified YES/NO task DAG.
// It encodes the same verification methodology as the system prompt (Section 1-3):
// decompose into yes/no leaf propositions, verify post-order, backtrack on NO.
//
// For unstructured/messy data (OCR, scanned PDFs, logs), the first task MUST be a
// data-probe: sample the data, identify noise patterns, report actual data quality.
// The decomposition then adapts based on what the probe found — not what was assumed.
const decomposePrompt = `Break the goal into a dependency-ordered task DAG. Output ONLY JSON.

PHASE 0 — DATA PROBE (always first, before any extraction attempt):
If the input is unstructured text, OCR output, scanned content, or logs:
  - FIRST TASK: "probe-data": read a sample, identify encoding issues, OCR artifacts,
    formatting quirks, and report: (a) is this machine-generated or human/scanned?
    (b) what noise patterns repeat? (c) can simple regex work, or does this need
    fuzzy/AI methods? This task MUST complete before any extraction tasks begin.
  - Extraction tasks must depend_on "probe-data" and adapt their method based on
    the probe results.

PHASE 1 — DECOMPOSITION (after probe):
1. Decompose into YES/NO propositions — each task answers a single verifiable question.
2. METHOD LADDER — per task, pick the right tool for the data quality:
   - Clean machine output → regex/grep is fine
   - OCR with predictable errors → regex with fuzzy character classes + normalization
   - Highly noisy/unreliable → avoid regex entirely; use AI text processing or manual
     line-by-line heuristics with explicit error tolerance
3. VERIFY your answer — concrete evidence (file:line, count, diff).
4. COVERAGE CHECK — if all YES, is the goal fully answered?
5. 3-7 tasks ideal, max 12.

Each task: {"id": "verb-noun", "description": "Verify: <proposition>", "depends_on": ["probe-data", ...]}

BACKTRACK PROTOCOL:
When a leaf returns NO, do NOT refine the same regex. Switch method tiers:
  regex failed → try fuzzy matching with character normalization
  fuzzy failed → try line-by-line heuristics with tolerance
  heuristics failed → try AI-assisted extraction
Report what you tried and what failed, so the next re-decomposition learns.`

// decomposeRequest is the JSON structure we expect back from the decomposer LLM.
type decomposeRequest struct {
	Tasks []struct {
		ID          string   `json:"id"`
		Description string   `json:"description"`
		DependsOn   []string `json:"depends_on"`
	} `json:"tasks"`
}

// Reasoner decomposes a goal into a task DAG via an LLM planner, then executes
// it with Planner's topological concurrency. It satisfies the Runner interface
// so it slots in as a drop-in replacement for Agent or Coordinator.
//
// The dispatch function executes individual PlanTasks — typically via
// RunSubAgent with the executor's provider/registry, or via a Team's
// orchestrator. The caller (boot) wires it.
type Reasoner struct {
	plannerProv    provider.Provider
	plannerSess    *Session
	plannerPricing *provider.Pricing
	dispatch       PlannerRunner
	maxConcurrent  int
	temperature    float64
	sink           event.Sink
	lastResult     string // captured by Run, returned by Reason
}

// NewReasoner creates a Reasoner. plannerProv and plannerSess are the
// decomposition LLM (kept in its own session for cache-stable prefix).
// dispatch is called for each ready PlanTask; maxConcurrent caps parallel
// dispatches. sink receives phase/text/usage events from the decomposer.
func NewReasoner(
	plannerProv provider.Provider,
	plannerSess *Session,
	plannerPricing *provider.Pricing,
	dispatch PlannerRunner,
	maxConcurrent int,
	temperature float64,
	sink event.Sink,
) *Reasoner {
	if sink == nil {
		sink = event.Discard
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	return &Reasoner{
		plannerProv:    plannerProv,
		plannerSess:    plannerSess,
		plannerPricing: plannerPricing,
		dispatch:       dispatch,
		maxConcurrent:  maxConcurrent,
		temperature:    temperature,
		sink:           sink,
	}
}

// Run decomposes the goal into a Plan DAG, then executes it with Planner.
// Falls back to a single-task plan if decomposition fails, so the executor
// still gets a chance to handle the goal.
// The aggregated result is captured and available via Reason().
func (r *Reasoner) Run(ctx context.Context, goal string) error {
	r.sink.Emit(&event.Event{Kind: event.TurnStarted})
	r.sink.Emit(&event.Event{Kind: event.Phase, Text: r.plannerProv.Name() + " · decomposing"})

	plan, err := r.decompose(ctx, goal)
	if err != nil {
		r.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: fmt.Sprintf("decomposition failed (%v) — running as single task", err)})
		// Fallback: single-task plan dispatched directly.
		plan = NewPlan(goal)
		plan.AddTask("main", goal)
	}

	r.sink.Emit(&event.Event{Kind: event.Phase, Text: fmt.Sprintf("executing %d tasks", len(plan.Tasks))})

	// Execute with re-decomposition: when the Planner exhausts retries, feed the
	// failure context back to the decomposer so it tries a different approach.
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		dag := NewPlanner()
		result, err := dag.Execute(ctx, plan, r.dispatch, r.maxConcurrent)
		r.lastResult = result
		lastErr = err

		if err == nil {
			if result != "" {
				r.sink.Emit(&event.Event{Kind: event.Text, Text: result})
			}
			return nil
		}

		// Build verification tree from task results, then use the method
		// ladder to generate precise re-decomposition guidance.
		vt, ladderHints := r.buildVerificationTree(plan)
		if vt != nil {
			r.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo,
				Text: vt.Summary()})
		}

		// Re-decompose on failure — feed the failure context back so the decomposer
		// learns what didn't work and climbs the method ladder.
		if attempt < 2 {
			r.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo,
				Text: fmt.Sprintf("re-decomposing after failure (attempt %d): %v", attempt+1, err)})
			// Build a re-decomposition prompt that carries diagnosis + method-ladder guidance.
			reGoal := goal +
				fmt.Sprintf("\n\nPREVIOUS ATTEMPT FAILED:\n  Error: %v\n  Summary of completed+failed tasks:\n%s\n\n"+
					"RE-DECOMPOSE: the failed subtasks need a DIFFERENT method tier.\n"+
					"%s\n"+
					"Preserve tasks that SUCCEEDED; only re-decompose what FAILED.",
					err, plan.Summary(), ladderHints)
			newPlan, reErr := r.decompose(ctx, reGoal)
			if reErr == nil {
				plan = newPlan
				continue
			}
			r.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
				Text: fmt.Sprintf("re-decomposition failed (%v), retrying original plan", reErr)})
		}
	}

	// All re-decomposition attempts exhausted.
	if r.lastResult != "" {
		r.sink.Emit(&event.Event{Kind: event.Text, Text: r.lastResult})
	}
	r.sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
		Text: fmt.Sprintf("plan execution failed after %d attempts", 3)})
	return lastErr
}

// Reason decomposes and executes like Run, but returns the aggregated result
// string alongside any error — see the multi-agent protocol in REASONIX.md.
func (r *Reasoner) Reason(ctx context.Context, goal string) (string, error) {
	err := r.Run(ctx, goal)
	return r.lastResult, err
}

// decompose streams a JSON task DAG from the decomposition model and parses it
// into a Plan. On any failure (network, parse, empty/trivial output) it returns
// an error so the caller can fall back.
func (r *Reasoner) decompose(ctx context.Context, goal string) (*Plan, error) {
	r.plannerSess.Add(provider.Message{Role: provider.RoleUser, Content: decomposePrompt + "\n\nGoal: " + goal})

	ch, err := r.plannerProv.Stream(ctx, provider.Request{
		Messages:    r.plannerSess.Snapshot(),
		Temperature: r.temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("decompose stream: %w", err)
	}

	var raw strings.Builder
	var usage *provider.Usage
	for chunk := range ch {
		switch chunk.Type {
		case provider.ChunkText:
			raw.WriteString(chunk.Text)
			r.sink.Emit(&event.Event{Kind: event.Text, Text: chunk.Text})
		case provider.ChunkUsage:
			usage = chunk.Usage
		case provider.ChunkError:
			return nil, chunk.Err
		default: // unknown outcome — ignore
		}
	}

	if usage != nil {
		r.sink.Emit(&event.Event{Kind: event.Usage, Usage: usage, Pricing: r.plannerPricing})
	}

	text := strings.TrimSpace(raw.String())
	if text == "" {
		return nil, fmt.Errorf("decomposer returned empty response")
	}

	// Store the response in the planner session for cache-stable prefix.
	r.plannerSess.Add(provider.Message{Role: provider.RoleAssistant, Content: text})

	plan, err := parseDecomposeResponse(text, goal)
	if err != nil {
		return nil, fmt.Errorf("parse decompose response: %w", err)
	}
	if len(plan.Tasks) == 0 {
		return nil, fmt.Errorf("decomposer produced no tasks")
	}
	return plan, nil
}

// buildVerificationTree converts the Plan's tasks into a reasoner.VerificationTree,
// parses each task's result for YES/NO verdicts, computes the root gate, and
// returns the tree plus method-ladder hints for failed leaves.
func (r *Reasoner) buildVerificationTree(plan *Plan) (*reasoner.VerificationTree, string) {
	plan.mu.Lock()
	defer plan.mu.Unlock()

	// Convert PlanTask → reasoner.TaskNode.
	nodes := make([]reasoner.TaskNode, 0, len(plan.Tasks))
	verdicts := make(map[string]reasoner.TaskVerdict, len(plan.Tasks))

	for _, t := range plan.Tasks {
		nodes = append(nodes, reasoner.TaskNode{
			ID:        t.ID,
			DependsOn: t.DependsOn,
		})
		v, evidence := reasoner.ParseVerdict(t.Result)
		// If the task was marked as failed by the Planner, treat as NO
		// regardless of parsed output.
		if t.Status == TaskFailed {
			v = reasoner.VerdictNo
		}
		verdicts[t.ID] = reasoner.TaskVerdict{
			TaskID:   t.ID,
			Verdict:  v,
			Evidence: evidence,
			Output:   t.Result,
		}
	}

	tree := reasoner.BuildTree(nodes, verdicts)
	tree.ComputeRootVerdict()

	// Generate method-ladder hints for failed leaves.
	failedLeaves := tree.FailedLeaves()
	if len(failedLeaves) == 0 {
		return tree, ""
	}

	var hints strings.Builder
	for _, leaf := range failedLeaves {
		// Infer the previous method from the task description or result.
		prevMethod := inferMethod(plan, leaf.TaskID)
		ladder := reasoner.MethodLadder{Current: prevMethod}
		hints.WriteString(ladder.SuggestPrompt(leaf.TaskID, prevMethod))
		hints.WriteString("\n")
	}
	return tree, hints.String()
}

// inferMethod guesses which method tier a task used, based on its description
// and result text. Falls back to MethodRegex as the default starting tier.
func inferMethod(plan *Plan, taskID string) reasoner.Method {
	for _, t := range plan.Tasks {
		if t.ID != taskID {
			continue
		}
		combined := strings.ToLower(t.Description + " " + t.Result)
		if strings.Contains(combined, "fuzzy") || strings.Contains(combined, "normaliz") {
			return reasoner.MethodFuzzy
		}
		if strings.Contains(combined, "heuristic") || strings.Contains(combined, "line-by-line") || strings.Contains(combined, "tolerance") {
			return reasoner.MethodHeuristic
		}
		if strings.Contains(combined, "ai") || strings.Contains(combined, "llm") || strings.Contains(combined, "assisted") {
			return reasoner.MethodAI
		}
	}
	return reasoner.MethodRegex // default starting tier
}

// parseDecomposeResponse extracts a Plan from the LLM's JSON output. It handles
// common formatting quirks like markdown code fences.
func parseDecomposeResponse(raw, goal string) (*Plan, error) {
	// Strip markdown fences if present.
	jsonStr := raw
	if idx := strings.Index(jsonStr, "```json"); idx >= 0 {
		jsonStr = jsonStr[idx+7:]
		if end := strings.LastIndex(jsonStr, "```"); end >= 0 {
			jsonStr = jsonStr[:end]
		}
	} else if idx := strings.Index(jsonStr, "```"); idx >= 0 {
		jsonStr = jsonStr[idx+3:]
		if end := strings.LastIndex(jsonStr, "```"); end >= 0 {
			jsonStr = jsonStr[:end]
		}
	}
	jsonStr = strings.TrimSpace(jsonStr)
	if jsonStr == "" {
		return nil, fmt.Errorf("empty JSON after stripping fences")
	}

	// Try to find the JSON object.
	if idx := strings.Index(jsonStr, "{"); idx >= 0 {
		jsonStr = jsonStr[idx:]
	}
	if idx := strings.LastIndex(jsonStr, "}"); idx >= 0 {
		jsonStr = jsonStr[:idx+1]
	}

	var req decomposeRequest
	if err := json.Unmarshal([]byte(jsonStr), &req); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w (raw: %.200s)", err, jsonStr)
	}

	plan := NewPlan(goal)
	for _, t := range req.Tasks {
		if t.ID == "" || t.Description == "" {
			continue
		}
		plan.AddTask(t.ID, t.Description, t.DependsOn...)
	}
	return plan, nil
}
