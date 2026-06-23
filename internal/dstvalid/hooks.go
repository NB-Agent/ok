// Package dstvalid implements synchronous compile/test regression hooks.
// Every write_file / edit_file / multi_edit triggers a go build (and optionally
// go test) in PostToolUse. Passes are recorded into a ProofChain for per‑turn
// agent memory; failures roll back the file via snapshot and surface a
// "rolled back: ..." error through ConsumeRollback.
//
// No async LLM verification — the proof chain alone gives the agent a compact,
// deduplicated summary of what has been verified across turns.
package dstvalid

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/winhide"
)

// writerNames is the set of write tools that need snapshotting and verification.
var writerNames = map[string]bool{
	"edit_file":  true,
	"write_file": true,
	"multi_edit": true,
}

// DSTHooks implements agent.ToolHooks with synchronous compile/test checks.
// Access to lastCompilePass / lastTestPass is guarded by passMu — a belt-and-
// suspenders measure against a future where writer tools may run in parallel.
type DSTHooks struct {
	snapshot *Snapshot
	workDir  string

	compileCmd string // e.g. "go build ./..."
	testCmd    string // e.g. "go test ./..."

	// Injectable check functions (for tests). nil = use default runCompileCheck / runTestCheck.
	compileFn func(context.Context) (bool, string)
	testFn    func(context.Context) (bool, string)

	// notice callback for user-facing DST status.
	Noticef func(format string, args ...interface{})

	// enabled gates compile/test checks; when false, PreToolUse/PostToolUse
	// skip all DST work. Toggled via Enable()/Disable() from the DSTRunner.
	// atomic so the control path can toggle it concurrently with the run loop.
	enabled atomic.Bool

	// Regression state: track pass→fail transitions for rollback.
	passMu          sync.Mutex
	lastCompilePass bool
	lastTestPass    bool

	// Proof chain for per‑turn memory injection.
	proofChain *core.ProofChain

	// Rollback state (set by rollbackAndLog, consumed by ConsumeRollback).
	rbMu       sync.Mutex
	rbReason   string // "compile", "test"
	rbDetail   string
	rbHappened bool

	next agent.ToolHooks
}

// NewDSTHooks creates a DST hooks instance. DST checks start enabled.
func NewDSTHooks(workDir string) *DSTHooks {
	h := &DSTHooks{
		snapshot:        NewSnapshot(),
		workDir:         workDir,
		lastCompilePass: true, // optimistic: project initially compiles
		lastTestPass:    true,
	}
	h.enabled.Store(true)
	return h
}

// Enable turns on compile/test checks.
func (h *DSTHooks) Enable() { h.enabled.Store(true) }

// Disable turns off compile/test checks and clears the current snapshot
// so a later re-enable starts fresh rather than replaying stale state.
func (h *DSTHooks) Disable() {
	h.enabled.Store(false)
	h.snapshot.Clear()
}

// IsEnabled reports whether compile/test checks are active.
func (h *DSTHooks) IsEnabled() bool { return h.enabled.Load() }

// SetWorkDir sets the working directory for compile/test checks.
func (h *DSTHooks) SetWorkDir(dir string) {
	h.workDir = dir
}

// SetBuildCommands sets the compile and test commands.
func (h *DSTHooks) SetBuildCommands(compile, test string) {
	h.compileCmd = compile
	h.testCmd = test
}

// SetProofChain wires a proof chain for recording compile/test passes.
func (h *DSTHooks) SetProofChain(pc *core.ProofChain) {
	h.proofChain = pc
}

// SetNoticeSink sets a callback for DST status messages.
func (h *DSTHooks) SetNoticeSink(notify func(string)) {
	if notify == nil {
		h.Noticef = nil
	} else {
		h.Noticef = func(format string, args ...interface{}) {
			notify(fmt.Sprintf(format, args...))
		}
	}
}

// SetNext sets the chained hooks.
func (h *DSTHooks) SetNext(next agent.ToolHooks) {
	h.next = next
}

// Next returns the chained hooks, or nil.
func (h *DSTHooks) Next() agent.ToolHooks { return h.next }

// ─── agent.ToolHooks ──────────────────────────────────────────────────

// ConsumeRollback checks and drains the rollback event. Called by Agent.executeOne
// after PostToolUse to inject the rollback message into the tool result.
func (h *DSTHooks) ConsumeRollback() (reason, detail string, happened bool) {
	h.rbMu.Lock()
	defer h.rbMu.Unlock()
	if h.rbHappened {
		h.rbHappened = false
		return h.rbReason, h.rbDetail, true
	}
	return "", "", false
}

// PreToolUse snapshots files before they are written.
func (h *DSTHooks) PreToolUse(ctx context.Context, name string, args json.RawMessage) (bool, string) {
	if !h.enabled.Load() {
		if h.next != nil {
			return h.next.PreToolUse(ctx, name, args)
		}
		return false, ""
	}
	if writerNames[name] {
		path := extractPathFromArgs(args)
		if path == "" {
			return false, ""
		}
		if err := h.snapshot.Capture(path); err != nil {
			return true, fmt.Sprintf("DST snapshot failed: %v", err)
		}
	}
	if h.next != nil {
		return h.next.PreToolUse(ctx, name, args)
	}
	return false, ""
}

// PostToolUse runs compile/test checks after a write, records passes in the
// proof chain, and rolls back on regression (pass→fail).
func (h *DSTHooks) PostToolUse(ctx context.Context, name string, args json.RawMessage, result string) {
	if h.next != nil {
		defer h.next.PostToolUse(ctx, name, args, result)
	}
	if !h.enabled.Load() || !writerNames[name] {
		return
	}

	filePath := extractPathFromArgs(args)

	// Compile check — only when the file is inside the project and the
	// relevant build tooling exists.
	shouldCompile := h.compileCmd != "" || h.compileFn != nil
	if shouldCompile && filePath != "" && h.workDir != "" {
		clean := filepath.Clean(filePath)
		wd := h.workDir
		// Case-insensitive comparison on case-insensitive filesystems (Windows, macOS).
		// filepath.Clean doesn't normalise case, but HasPrefix and index arithmetic do.
		// Lowercasing both avoids silent DST skips on C:\Users vs c:\users mismatches.
		if !filepath.IsAbs(clean) {
			if abs, err := filepath.Abs(clean); err == nil {
				clean = abs
			}
		}
		cl := strings.ToLower(clean)
		wl := strings.ToLower(wd)
		shouldCompile = strings.HasPrefix(cl, wl) &&
			(len(cl) == len(wl) || cl[len(wl)] == filepath.Separator)
	}
	if shouldCompile && strings.HasPrefix(h.compileCmd, "go ") {
		if _, err := os.Stat(filepath.Join(h.workDir, "go.mod")); os.IsNotExist(err) {
			shouldCompile = false
		}
	}
	if shouldCompile {
		if h.Noticef != nil {
			h.Noticef("compile check: %s", filePath)
		}
		postPass, postLog := h.runCompileCheck(ctx)
		h.passMu.Lock()
		if !postPass && h.lastCompilePass {
			h.passMu.Unlock()
			if h.Noticef != nil {
				h.Noticef("COMPILE FAILED — rolling back %s", filePath)
			}
			h.rollbackAndLog(name, "compile", postLog)
			if rePass, reLog := h.runCompileCheck(ctx); rePass {
				h.passMu.Lock()
				h.lastCompilePass = true
				h.passMu.Unlock()
			} else {
				if reLog != "" {
					fmt.Fprintf(os.Stderr, "dstvalid: recompile check: %s\n", reLog)
				}
				h.passMu.Lock()
				h.lastCompilePass = false
				h.passMu.Unlock()
			}
			// Record failure in proof chain for agent memory.
			if h.proofChain != nil {
				h.proofChain.Append("compile", fmt.Sprintf("%s compiles after %s", filePath, name), "FAIL: "+postLog)
			}
			return
		}
		h.lastCompilePass = postPass
		h.passMu.Unlock()
		if h.Noticef != nil {
			h.Noticef("compile check: PASS")
		}
		if postPass && h.proofChain != nil {
			h.proofChain.Append("compile", fmt.Sprintf("go build after %s", filePath), "OK")
		}
	}

	// Test check (optional).
	if h.testCmd != "" || h.testFn != nil {
		if h.Noticef != nil {
			h.Noticef("test check...")
		}
		postPass, postLog := h.runTestCheck(ctx)
		h.passMu.Lock()
		if !postPass && h.lastTestPass {
			h.passMu.Unlock()
			if h.Noticef != nil {
				h.Noticef("TEST FAILED — rolling back")
			}
			h.rollbackAndLog(name, "test", postLog)
			if rePass, reLog := h.runTestCheck(ctx); rePass {
				h.passMu.Lock()
				h.lastTestPass = true
				h.passMu.Unlock()
			} else {
				if reLog != "" {
					fmt.Fprintf(os.Stderr, "dstvalid: retest check: %s\n", reLog)
				}
				h.passMu.Lock()
				h.lastTestPass = false
				h.passMu.Unlock()
			}
			if h.proofChain != nil {
				h.proofChain.Append("test", fmt.Sprintf("go test after %s", filePath), "FAIL: "+postLog)
			}
			return
		}
		h.lastTestPass = postPass
		h.passMu.Unlock()
		if h.Noticef != nil {
			h.Noticef("test check: PASS")
		}
		if postPass && h.proofChain != nil {
			h.proofChain.Append("test", fmt.Sprintf("go test after %s", filePath), "OK")
		}
	}

	h.snapshot.Clear()
}

// ─── internal helpers ────────────────────────────────────────────────────

func (h *DSTHooks) runCompileCheck(ctx context.Context) (bool, string) {
	if h.compileFn != nil {
		return h.compileFn(ctx)
	}
	if h.compileCmd == "" || h.workDir == "" {
		return true, ""
	}
	argv, err := splitCommand(h.compileCmd)
	if err != nil {
		return false, fmt.Sprintf("invalid compile command: %v", err)
	}
	cmd := winhide.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = h.workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, string(out)
	}
	return true, ""
}

func (h *DSTHooks) runTestCheck(ctx context.Context) (bool, string) {
	if h.testFn != nil {
		return h.testFn(ctx)
	}
	if h.testCmd == "" || h.workDir == "" {
		return true, ""
	}
	argv, err := splitCommand(h.testCmd)
	if err != nil {
		return false, fmt.Sprintf("invalid test command: %v", err)
	}
	cmd := winhide.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = h.workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, string(out)
	}
	return true, ""
}

func (h *DSTHooks) rollbackAndLog(toolName, checkType, output string) {
	if err := h.snapshot.Rollback(); err != nil {
		h.rbMu.Lock()
		h.rbReason = checkType
		h.rbDetail = fmt.Sprintf("ROLLBACK FAILED: %v\n%s", err, output)
		h.rbHappened = true
		h.rbMu.Unlock()
		fmt.Fprintf(os.Stderr, "DSTHooks: rollback error after %s failure: %v\n", checkType, err)
		return
	}
	// Rollback succeeded — clear snapshot so stale data isn't reused
	// on the next Capture failure (which would cause a double rollback).
	h.snapshot.Clear()
	msg := fmt.Sprintf("%s check failed after %s, rolled back:\n%s", checkType, toolName, output)
	fmt.Fprintf(os.Stderr, "DSTHooks: %s\n", msg)

	h.rbMu.Lock()
	h.rbReason = checkType
	h.rbDetail = output
	h.rbHappened = true
	h.rbMu.Unlock()
}

// ─── utility functions ───────────────────────────────────────────────────

func extractPathFromArgs(args json.RawMessage) string {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return ""
	}
	return p.Path
}

func splitCommand(cmd string) ([]string, error) {
	// DST compile/test commands run through exec.CommandContext, which invokes
	// the command directly without a shell. Shell operators (&&, ||, |, >, <,
	// >>, ;, `) would be passed as literal arguments and not work. Auto-wrap
	// in the system shell when such operators are detected.
	for _, op := range []string{"&&", "||", "|", ">", "<", ">>", ";", "`"} {
		if strings.Contains(cmd, op) {
			shell, arg := "sh", "-c"
			if runtime.GOOS == "windows" {
				shell, arg = "cmd", "/c"
			}
			return []string{shell, arg, cmd}, nil
		}
	}
	var parts []string
	var current strings.Builder
	inSingle, inDouble := false, false
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case ch == ' ' && !inSingle && !inDouble:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return parts, nil
}
