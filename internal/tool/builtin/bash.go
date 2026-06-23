package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/sandbox"
	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/winhide"
)

const bashTimeout = 300 * time.Second

func init() { tool.RegisterBuiltin(bash{}) }

// bash runs a shell command with a timeout to avoid hangs. sb, when it enforces,
// wraps the command in an OS sandbox; the zero value registered at init runs
// unconfined and is overridden per run by ConfineBash. workDir, when non-empty,
// is the directory the command runs in (cmd.Dir); empty uses the process cwd.
type bash struct {
	sb      sandbox.Spec
	workDir string
}

func (bash) Name() string { return "bash" }

func (bash) Description() string {
	return "Execute a shell command and return combined stdout/stderr. Use for builds, tests, git, etc."
}

func (bash) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"command":{"type":"string"},"run_in_background":{"type":"boolean"}},"required":["command"],"type":"object"}`)
}

// ReadOnly is false: bash's effect cannot be inferred from args (rm, curl,
// git commit, etc. are all reachable). Conservative even when a particular
// command happens to be read-only — the agent batch decision can't tell.
func (bash) ReadOnly() bool { return false }

func (b bash) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Command         string `json:"command"`
		RunInBackground bool   `json:"run_in_background"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	// Security: check command against allow/deny lists
	if err := CheckCommand(p.Command); err != nil {
		return "", fmt.Errorf("bash: %w", err)
	}

	// Wrap in the OS sandbox when configured; otherwise argv is just bash -c.
	argv, sandboxed := sandbox.Command(b.sb, "bash", p.Command)

	if p.RunInBackground {
		jm, ok := jobs.FromContext(ctx)
		if !ok {
			return "", fmt.Errorf("background execution is not available in this context")
		}
		workDir := b.workDir
		// The job runs under the manager's session context (no 120s timeout), so it
		// survives this turn; its combined output streams to the job buffer.
		job := jm.Start("bash", commandPreview(p.Command), func(jobCtx context.Context, out io.Writer) (string, error) {
			cmd := winhide.CommandContext(jobCtx, argv[0], argv[1:]...)
			cmd.Dir = workDir
			cmd.Stdout = out
			cmd.Stderr = out
			if sandboxed {
				if err := cmd.Start(); err != nil {
					return "", err
				}
				if err := sandbox.WrapProcess(cmd.Process.Pid, b.sb); err != nil {
					fmt.Fprintf(os.Stderr, "bash: sandbox wrap (background): %v\n", err)
					if kerr := cmd.Process.Kill(); kerr != nil {
						fmt.Fprintf(os.Stderr, "bash: kill after wrap fail: %v\n", kerr)
					}
					if werr := cmd.Wait(); werr != nil {
						fmt.Fprintf(os.Stderr, "bash: wait after kill: %v\n", werr)
					}
					return "", fmt.Errorf("sandbox wrap failed: %w", err)
				}
				return "", cmd.Wait()
			}
			return "", cmd.Run()
		})
		return fmt.Sprintf("Started background job %q. It keeps running across turns; read new output with bash_output(job_id=%q), wait for it with wait, or stop it with kill_shell(job_id=%q).", job.ID, job.ID, job.ID), nil
	}

	ctx, cancel := context.WithTimeout(ctx, bashTimeout)
	defer cancel()

	cmd := winhide.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = b.workDir // "" lets exec use the process working directory
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	var err error
	if sandboxed {
		if startErr := cmd.Start(); startErr != nil {
			return "", fmt.Errorf("start command: %w", startErr)
		}
		if err := sandbox.WrapProcess(cmd.Process.Pid, b.sb); err != nil {
			if kerr := cmd.Process.Kill(); kerr != nil {
				fmt.Fprintf(os.Stderr, "bash: kill after wrap fail: %v\n", kerr)
			}
			if werr := cmd.Wait(); werr != nil {
				fmt.Fprintf(os.Stderr, "bash: wait after kill: %v\n", werr)
			}
			return "", fmt.Errorf("sandbox wrap: %w", err)
		}
		err = cmd.Wait()
	} else {
		err = cmd.Run()
	}
	out := truncateBash(buf.String())

	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("command timed out (> %s)", bashTimeout)
	}
	if err != nil {
		// Non-zero exit: feed output and error back so the model can self-correct.
		return out, fmt.Errorf("command exited: %w", err)
	}
	return out, nil
}

// truncateBash preserves head+tail of long command output so the model sees
// both the command's early output and its final result/error, without
// bloating the session history with repeated intermediate lines (e.g.
// hundreds of "ok  pkg" lines). Lines are the natural unit for terminal
// output and avoid splitting multi-byte characters.
func truncateBash(s string) string {
	const (
		maxLines  = 60
		headLines = 20
		tailLines = 20
	)
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	n := len(lines)
	head := strings.Join(lines[:headLines], "\n")
	tail := strings.Join(lines[n-tailLines:], "\n")
	// Strip trailing empty line from Split before counting — it's an artifact,
	// not a real line.
	realLines := len(lines)
	if realLines > 0 && lines[realLines-1] == "" {
		realLines--
	}
	skipped := realLines - headLines - tailLines
	if skipped <= 0 {
		return s
	}
	return head + fmt.Sprintf("\n\n... (%d lines truncated — re-run with a more specific command for full output)\n\n", skipped) + tail
}

// commandPreview is a short single-line label for a background bash job, surfaced
// in the status bar and completion notices. Truncated at 48 runes to fit in a
// standard 80-column terminal status line alongside other fields.
func commandPreview(cmd string) string {
	cmd = strings.TrimSpace(strings.ReplaceAll(cmd, "\n", " "))
	const max = 48
	r := []rune(cmd)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return cmd
}
