package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/evidence"
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
//
// When an evidence.Ledger is available in the context, verification and diff/files
// evidence items are cross-checked against the current turn's actual tool receipts.
// A verification command that was never run (or ran but failed), or a path that was
// never written/read by a tool in this same turn, causes the step to be rejected.
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
	return "Sign off a plan step with evidence. Pair with todo_write."
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
	hostVerified := 0
	manualUnverified := 0
	for i, e := range p.Evidence {
		if !validEvidenceKinds[e.Kind] {
			return "", fmt.Errorf("evidence %d: invalid kind %q (want verification|diff|files|manual)", i+1, e.Kind)
		}
		if strings.TrimSpace(e.Summary) == "" {
			return "", fmt.Errorf("evidence %d: summary is required — the evidence is the summary, not just its kind", i+1)
		}
		if e.Kind != "manual" {
			if err := verifyStepEvidence(ctx, e, i); err != nil {
				return "", err
			}
			hostVerified++
		} else {
			manualUnverified++
		}
		kinds = append(kinds, e.Kind)
	}
	hostStatus := ""
	if hostVerified > 0 || manualUnverified > 0 {
		hostStatus = fmt.Sprintf(" Host evidence: host-verified %d, manual/unverified %d.", hostVerified, manualUnverified)
	}
	return fmt.Sprintf("Step %q signed off with %d evidence item(s) [%s].%s Move the next step to in_progress with todo_write.",
		p.Step, len(p.Evidence), strings.Join(kinds, ", "), hostStatus), nil
}

// verifyStepEvidence cross-checks a non-manual evidence item against the
// evidence ledger in ctx. When the ledger is absent, the step is accepted
// without host verification — evidence is still required but isn't cross-checked.
func verifyStepEvidence(ctx context.Context, e stepEvidence, i int) error {
	ledger, ok := evidence.FromContext(ctx)
	if !ok || ledger == nil {
		return nil // no ledger — accept without cross-checking
	}
	switch e.Kind {
	case "verification":
		command := strings.TrimSpace(e.Command)
		if command == "" {
			return fmt.Errorf("evidence %d: verification command is required for host verification — cite the command you ran, or use kind \"manual\"", i+1)
		}
		if ledger.HasFailedCommand(command) {
			return fmt.Errorf("evidence %d: verification command %q ran but exited non-zero, so it can't prove the step; re-run so it succeeds and sign off again", i+1, command)
		}
		if !ledger.HasSuccessfulCommand(command) {
			return fmt.Errorf("evidence %d: verification command %q has no matching successful bash receipt in this turn — run the command first, then sign off", i+1, command)
		}
	case "diff":
		if len(e.Paths) == 0 {
			return fmt.Errorf("evidence %d: diff evidence requires paths for host verification — cite the files you changed", i+1)
		}
		if !ledger.HasSuccessfulWrite(e.Paths) {
			return fmt.Errorf("evidence %d: diff paths have no matching successful writer receipt in this turn — edit the files first, then sign off", i+1)
		}
	case "files":
		if len(e.Paths) == 0 {
			return fmt.Errorf("evidence %d: files evidence requires paths for host verification — cite the files you touched", i+1)
		}
		if !ledger.HasSuccessfulReadOrWrite(e.Paths) {
			return fmt.Errorf("evidence %d: file paths have no matching successful read/write receipt in this turn", i+1)
		}
	}
	return nil
}
