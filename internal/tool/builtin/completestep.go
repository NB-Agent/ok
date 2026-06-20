package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(completeStep{}) }

// completeStep records an evidence-backed completion of one step of an approved
// plan. Like todo_write it has no host side effects — the claim and its evidence
// live in the call's args, which a frontend renders as a signed-off step. Its
// reason for existing is the enforcement in Execute: a completion with no evidence
// is rejected, so the model can't flip a step to "done" without showing why it is
// done (the verification it ran, the diff/files it changed, or a manual check).
// It complements todo_write — todo_write keeps the list moving (one item
// in_progress), complete_step is the formal sign-off of a finished step.
type completeStep struct{}

type stepEvidence struct {
	Kind    string   `json:"kind"`
	Summary string   `json:"summary"`
	Command string   `json:"command,omitempty"`
	Paths   []string `json:"paths,omitempty"`
}

// validEvidenceKinds are the evidence forms a completion may cite. "checkpoint"
// (main's fourth kind) is omitted — v2 has no checkpoint system.
var validEvidenceKinds = map[string]bool{
	"verification": true, // a command/test was run; cite it and its outcome
	"diff":         true, // a concrete code change; cite what changed
	"files":        true, // files created/edited/inspected; cite the paths
	"manual":       true, // a manual check; cite what was confirmed and how
}

func (completeStep) Name() string { return "complete_step" }

func (completeStep) Description() string {
	return "Sign off a finished plan step with evidence (verification, diff, or manual check). Pair with todo_write."
}

func (completeStep) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"evidence":{"items":{"properties":{"command":{"type":"string"},"kind":{"enum":["verification","diff","files","manual"],"type":"string"},"paths":{"items":{"type":"string"},"type":"array"},"summary":{"type":"string"}},"required":["kind","summary"],"type":"object"},"minItems":1,"type":"array"},"notes":{"type":"string"},"result":{"type":"string"},"step":{"type":"string"}},"required":["step","result","evidence"],"type":"object"}`)
}

// ReadOnly is true: complete_step only records a claim (no filesystem or process
// effect), so it never needs approval and stays available alongside todo_write.
func (completeStep) ReadOnly() bool { return true }

func (completeStep) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Step     string         `json:"step"`
		Result   string         `json:"result"`
		Evidence []stepEvidence `json:"evidence"`
		Notes    string         `json:"notes"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Step) == "" {
		return "", fmt.Errorf("step is required — name the plan step you are completing")
	}
	if strings.TrimSpace(p.Result) == "" {
		return "", fmt.Errorf("result is required — state what is now true after finishing this step")
	}
	if len(p.Evidence) == 0 {
		return "", fmt.Errorf("at least one evidence item is required — don't mark a step complete without showing why it's done (run a check, cite the diff, or confirm manually)")
	}
	kinds := make([]string, 0, len(p.Evidence))
	for i, e := range p.Evidence {
		if !validEvidenceKinds[e.Kind] {
			return "", fmt.Errorf("evidence %d: invalid kind %q (want verification|diff|files|manual)", i+1, e.Kind)
		}
		if strings.TrimSpace(e.Summary) == "" {
			return "", fmt.Errorf("evidence %d: summary is required — the evidence is the summary, not just its kind", i+1)
		}
		kinds = append(kinds, e.Kind)
	}
	return fmt.Sprintf("Step %q signed off with %d evidence item(s) [%s]. Move the next step to in_progress with todo_write.",
		p.Step, len(p.Evidence), strings.Join(kinds, ", ")), nil
}
