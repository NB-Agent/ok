package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/tool"
)

// bash_output / kill_shell / wait operate the background jobs registered by
// bash(run_in_background) and task(run_in_background). They reach the session's
// job manager through the call context (jobs.FromContext) — the agent stamps it
// onto every tool call — and degrade to a clear error when it isn't available
// (a headless context with no manager). Together they poll a job's new output,
// terminate a job, and block until jobs finish.

func init() {
	tool.RegisterBuiltin(bashOutput{})
	tool.RegisterBuiltin(killShell{})
	tool.RegisterBuiltin(waitJob{})
}

// --- bash_output: poll a background job's new output (non-blocking) ---

type bashOutput struct{}

func (bashOutput) Name() string { return "bash_output" }

func (bashOutput) Description() string {
	return "Read new output from a background job since last call, plus status (running/done/failed/killed). Non-blocking."
}

func (bashOutput) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"filter":{"type":"string"},"job_id":{"type":"string"}},"required":["job_id"],"type":"object"}`)
}

func (bashOutput) ReadOnly() bool { return true }

func (bashOutput) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		JobID  string `json:"job_id"`
		Filter string `json:"filter"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.JobID == "" {
		return "", fmt.Errorf("job_id is required")
	}
	jm, ok := jobs.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("background jobs are not available in this context")
	}
	text, status, found := jm.Output(p.JobID)
	if !found {
		return "", fmt.Errorf("no background job %q", p.JobID)
	}
	if p.Filter != "" && text != "" {
		filtered, err := filterLines(text, p.Filter)
		if err != nil {
			return "", err
		}
		text = filtered
	}
	header := fmt.Sprintf("[%s] %s", p.JobID, status)
	if strings.TrimSpace(text) == "" {
		return header + "\n(no new output)", nil
	}
	return header + "\n" + text, nil
}

// filterLines keeps only the lines of s matching the regular expression re.
func filterLines(s, re string) (string, error) {
	rx, err := regexp.Compile(re)
	if err != nil {
		return "", fmt.Errorf("invalid filter regexp: %w", err)
	}
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		if rx.MatchString(line) {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n"), nil
}

// --- kill_shell: terminate a running background job ---

type killShell struct{}

func (killShell) Name() string { return "kill_shell" }

func (killShell) Description() string {
	return "Terminate a running background job (bash or task). No-op if the job already finished or id is unknown."
}

func (killShell) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"job_id":{"type":"string"}},"required":["job_id"],"type":"object"}`)
}

func (killShell) ReadOnly() bool { return false }

func (killShell) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.JobID == "" {
		return "", fmt.Errorf("job_id is required")
	}
	jm, ok := jobs.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("background jobs are not available in this context")
	}
	if jm.Kill(p.JobID) {
		return fmt.Sprintf("Killed background job %q.", p.JobID), nil
	}
	return fmt.Sprintf("Background job %q was not running (already finished or unknown).", p.JobID), nil
}

// --- wait: block until background jobs finish, then return their results ---

type waitJob struct{}

func (waitJob) Name() string { return "wait" }

func (waitJob) Description() string {
	return "Block until background jobs finish, then return status+output. Omit job_ids to wait for all running jobs."
}

func (waitJob) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"job_ids":{"items":{"type":"string"},"type":"array"},"timeout_seconds":{"minimum":1,"type":"integer"}},"type":"object"}`)
}

func (waitJob) ReadOnly() bool { return true }

func (waitJob) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		JobIDs         []string `json:"job_ids"`
		TimeoutSeconds int      `json:"timeout_seconds"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	jm, ok := jobs.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("background jobs are not available in this context")
	}
	results := jm.Wait(ctx, p.JobIDs, p.TimeoutSeconds)
	if len(results) == 0 {
		return "No background jobs to wait for.", nil
	}
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n\n")
		}
		label := r.ID
		if r.Label != "" {
			label = fmt.Sprintf("%s (%s)", r.ID, r.Label)
		}
		fmt.Fprintf(&b, "[%s] %s", label, r.Status)
		if strings.TrimSpace(r.Output) != "" {
			b.WriteString("\n" + r.Output)
		}
	}
	return b.String(), nil
}
