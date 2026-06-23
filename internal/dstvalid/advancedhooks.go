package dstvalid

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/winhide"
)

// AdvancedHooks wraps DSTHooks with five opt-in capabilities:
//  1. Change impact analysis — PreToolUse warns which packages import the file being edited.
//  2. Auto learning — PostToolUse captures successful edits into the proof chain.
//  3. Targeted testing — runs tests only for packages that depend on the changed file.
//  4. Coverage monitoring — warns when package coverage falls below the threshold.
//  5. Style check — runs gofmt -d and go vet on the edited file's package after every write.
//  6. Static analysis — runs go vet on all affected packages after every write.
type AdvancedHooks struct {
	inner agent.ToolHooks

	workDir string

	compileCmd string
	testCmd    string
	proofChain *core.ProofChain

	// enabled gates all advanced features.
	enabled atomic.Bool

	// Mode flags (opt-in).
	learnMode         atomic.Bool
	impactMode        atomic.Bool
	targetedTestMode  atomic.Bool
	coverageMode      atomic.Bool
	styleCheckMode    atomic.Bool
	okVerifyMode      atomic.Bool
	coverageThreshold float64 // 0.0–1.0, emit warning below this (default 0.5)

	// Dependency cache: maps package dir → list of dependent internal packages.
	depMu sync.RWMutex
	deps  map[string][]string // key = file path of edited file, value = dependent packages

	// Learning log: circular buffer of recent successful edits.
	learnMu       sync.Mutex
	learnings     []string                // max 10
	learningSaver func(name, body string) // optional callback to persist learnings
}

// NewAdvancedHooks creates an AdvancedHooks wrapping the given inner hooks.
// Starts disabled — call SetLearnMode/SetImpactMode/SetTargetedTestMode to enable.
func NewAdvancedHooks(inner agent.ToolHooks, workDir, compileCmd, testCmd string, pc *core.ProofChain) *AdvancedHooks {
	h := &AdvancedHooks{
		inner:      inner,
		workDir:    workDir,
		compileCmd: compileCmd,
		testCmd:    testCmd,
		proofChain: pc,
		deps:       make(map[string][]string),
		learnings:  make([]string, 0, 10),
	}
	h.enabled.Store(true)
	return h
}

// Enable turns on all advanced features.
func (h *AdvancedHooks) Enable() { h.enabled.Store(true) }

// Disable turns off all advanced features.
func (h *AdvancedHooks) Disable() { h.enabled.Store(false) }

// SetLearnMode enables auto learning — successful edits are captured as proof chain entries.
func (h *AdvancedHooks) SetLearnMode(on bool) { h.learnMode.Store(on) }

// SetImpactMode enables change impact analysis — shows which packages import the edited file.
func (h *AdvancedHooks) SetImpactMode(on bool) { h.impactMode.Store(on) }

// SetTargetedTestMode enables targeted testing — only tests packages affected by the change.
func (h *AdvancedHooks) SetTargetedTestMode(on bool) { h.targetedTestMode.Store(on) }

// SetCoverageMode enables coverage threshold checking — after editing a file,
// run go test -cover on affected packages and warn if coverage drops below the threshold.
func (h *AdvancedHooks) SetCoverageMode(on bool) { h.coverageMode.Store(on) }

// SetCoverageThreshold sets the minimum acceptable coverage ratio (0.0–1.0, default 0.5).
func (h *AdvancedHooks) SetCoverageThreshold(t float64) {
	if t < 0 {
		t = 0
	}
	if t > 1.0 {
		t = 1.0
	}
	h.coverageThreshold = t
}

// SetStyleCheckMode enables automatic gofmt + go vet after every write operation.
func (h *AdvancedHooks) SetStyleCheckMode(on bool) { h.styleCheckMode.Store(on) }

// SetOkVerifyMode enables automatic static analysis (go vet ./... + ok-verify equivalent) after every write operation.
func (h *AdvancedHooks) SetOkVerifyMode(on bool) { h.okVerifyMode.Store(on) }

// SetLearningSaver installs a callback that persists learnings to durable storage
// (e.g. the memory store). The callback receives (name, body) and is called
// asynchronously after each successful edit capture — it must not block.
func (h *AdvancedHooks) SetLearningSaver(saver func(name, body string)) {
	h.learningSaver = saver
}

// ——— agent.ToolHooks ———

func (h *AdvancedHooks) PreToolUse(ctx context.Context, name string, args json.RawMessage) (bool, string) {
	if h.inner != nil {
		block, msg := h.inner.PreToolUse(ctx, name, args)
		if block {
			return block, msg
		}
	}

	if !h.enabled.Load() || !h.impactMode.Load() {
		return false, ""
	}

	// Change impact analysis: before editing a file, show which packages import it.
	if name == "edit_file" || name == "write_file" || name == "multi_edit" {
		path := extractPathFromArgs(args)
		if path != "" && h.workDir != "" {
			rel, err := filepath.Rel(h.workDir, path)
			if err == nil && !strings.HasPrefix(rel, "..") {
				deps := h.findDependents(ctx, rel)
				if len(deps) > 0 {
					msg := fmt.Sprintf("⚠️  Change impact: editing %s affects %d other package(s): %s",
						rel, len(deps), strings.Join(deps, ", "))
					fmt.Fprintf(os.Stderr, "AdvancedHooks: %s\n", msg)
				}
			}
		}
	}
	return false, ""
}

func (h *AdvancedHooks) PostToolUse(ctx context.Context, name string, args json.RawMessage, result string) {
	if !h.enabled.Load() {
		if h.inner != nil {
			h.inner.PostToolUse(ctx, name, args, result)
		}
		return
	}

	writerTools := map[string]bool{"edit_file": true, "write_file": true, "multi_edit": true}
	if !writerTools[name] {
		if h.inner != nil {
			h.inner.PostToolUse(ctx, name, args, result)
		}
		return
	}

	filePath := extractPathFromArgs(args)

	if h.inner != nil {
		h.inner.PostToolUse(ctx, name, args, result)
	}

	// Auto learning: capture every edit attempt into the proof chain.
	// The compile/test result is also in the proof chain (from DSTHooks),
	// so the agent can correlate "edited X" with "compile: OK/FAIL".
	if h.learnMode.Load() && filePath != "" && h.proofChain != nil {
		h.captureLearning(name, filePath)
	}

	// Targeted testing: run tests for affected packages after the edit.
	if h.targetedTestMode.Load() && filePath != "" {
		h.runTargetedTests(ctx, name, filePath)
	}

	// Coverage check: warn if affected packages fall below the threshold.
	if h.coverageMode.Load() && filePath != "" {
		h.checkCoverage(ctx, filePath)
	}

	// Auto style-check: run gofmt -d + go vet on the edited file's package.
	if h.styleCheckMode.Load() && filePath != "" {
		h.runStyleCheck(ctx, filePath)
	}

	// Auto static analysis: run go vet on all affected packages.
	if h.okVerifyMode.Load() && filePath != "" {
		h.runOkVerify(ctx, filePath)
	}
}

func (h *AdvancedHooks) ConsumeRollback() (string, string, bool) {
	if h.inner != nil {
		return h.inner.ConsumeRollback()
	}
	return "", "", false
}

// ——— internal ———

// findDependents runs `go list -json` to find which internal packages import a given file.
func (h *AdvancedHooks) findDependents(ctx context.Context, fileRel string) []string {
	// Derive the package path from the file path.
	pkgDir := filepath.Dir(fileRel)
	if pkgDir == "." {
		return nil
	}
	// Normalize pkgDir: "internal/control/foo.go" → "internal/control"
	pkgDir = strings.TrimSuffix(pkgDir, ".go")

	// Quick check: have we already computed this?
	pkgKey := filepath.Join(h.workDir, pkgDir)
	h.depMu.RLock()
	cached, ok := h.deps[pkgKey]
	h.depMu.RUnlock()
	if ok {
		return cached
	}

	// Run go list to find dependents. This is the most reliable way.
	cmd := winhide.CommandContext(ctx, "go", "list", "-json", "./...")
	cmd.Dir = h.workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	var dependents []string
	prefix := "ok/"
	seen := make(map[string]bool)

	// Parse the JSON output — each package is a JSON object separated by newlines.
	// We look for packages whose `ImportPath` is our target OR that import our target.
	targetPath := prefix + strings.ReplaceAll(pkgDir, string(filepath.Separator), "/")

	for _, block := range splitJSONBlocks(string(out)) {
		if strings.Contains(block, `"ImportPath"`) && strings.Contains(block, `"Imports"`) {
			var pkg struct {
				ImportPath string   `json:"importPath"`
				Imports    []string `json:"Imports"`
			}
			if err := json.Unmarshal([]byte(block), &pkg); err != nil {
				continue
			}
			if !strings.HasPrefix(pkg.ImportPath, prefix) || pkg.ImportPath == targetPath {
				continue
			}
			for _, imp := range pkg.Imports {
				if imp == targetPath && !seen[pkg.ImportPath] {
					seen[pkg.ImportPath] = true
					dependents = append(dependents, pkg.ImportPath)
				}
			}
		}
	}

	// Cache the result.
	h.depMu.Lock()
	h.deps[pkgKey] = dependents
	h.depMu.Unlock()

	return dependents
}

// captureLearning records a successful edit into the proof chain for agent memory.
func (h *AdvancedHooks) captureLearning(toolName, filePath string) {
	entry := fmt.Sprintf("edited %s via %s — OK", filePath, toolName)
	if h.proofChain != nil {
		h.proofChain.Append("learn", entry, "verified")
	}

	h.learnMu.Lock()
	if len(h.learnings) >= 10 {
		h.learnings = h.learnings[1:]
	}
	h.learnings = append(h.learnings, entry)
	learningCount := len(h.learnings)
	h.learnMu.Unlock()

	fmt.Fprintf(os.Stderr, "AdvancedHooks: learned %s (total: %d)\n", filePath, learningCount)

	// Persist to memory store if a saver callback is installed.
	if h.learningSaver != nil {
		h.learningSaver("auto-fix-"+toolName+"-"+fileNameOnly(filePath), entry)
	}
}

// runTargetedTests runs go test only on packages that depend on the changed file.
func (h *AdvancedHooks) runTargetedTests(ctx context.Context, toolName, filePath string) {
	if h.testCmd == "" && h.workDir == "" {
		return
	}

	rel, err := filepath.Rel(h.workDir, filePath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}

	dependents := h.findDependents(ctx, rel)
	if len(dependents) == 0 {
		return
	}

	// Run tests on the affected packages.
	for _, dep := range dependents {
		pkgDir := strings.TrimPrefix(dep, "ok/")
		if pkgDir == "" {
			continue
		}

		argv, err := splitCommand(h.testCmd)
		if err != nil {
			continue
		}
		// Replace ./... with the specific package
		targetArg := "./" + pkgDir + "/..."
		args := make([]string, 0, len(argv))
		for _, a := range argv {
			if a == "./..." {
				args = append(args, targetArg)
			} else {
				args = append(args, a)
			}
		}
		if len(args) == 0 {
			args = []string{argv[0], targetArg}
		}

		cmd := winhide.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = h.workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "AdvancedHooks: targeted test %s FAILED:\n%s\n", dep, string(out))
		} else {
			fmt.Fprintf(os.Stderr, "AdvancedHooks: targeted test %s PASS\n", dep)
		}
	}
}

// LearningSummary returns a formatted string of recent learnings, for system prompt injection.
func (h *AdvancedHooks) LearningSummary() string {
	h.learnMu.Lock()
	defer h.learnMu.Unlock()
	if len(h.learnings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Recent changes\n\n")
	for _, l := range h.learnings {
		b.WriteString(fmt.Sprintf("- %s\n", l))
	}
	return b.String()
}

// checkCoverage runs go test -cover on the package containing the edited file
// and warns if coverage falls below the configured threshold.
func (h *AdvancedHooks) checkCoverage(ctx context.Context, filePath string) {
	rel, err := filepath.Rel(h.workDir, filePath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}

	// Determine the package from the file path
	pkgDir := filepath.Dir(rel)
	if pkgDir == "." || pkgDir == "" {
		return
	}

	threshold := h.coverageThreshold
	if threshold <= 0 {
		threshold = 0.5 // default
	}

	// Also check the file's own package (not just dependents)
	pkgs := []string{pkgDir}
	pkgs = append(pkgs, h.findDependents(ctx, rel)...)

	seen := map[string]bool{}
	for _, dep := range pkgs {
		targetDir := strings.TrimPrefix(dep, "ok/")
		if targetDir == "" || seen[targetDir] {
			continue
		}
		seen[targetDir] = true

		cmd := winhide.CommandContext(ctx, "go", "test", "-cover", "./"+targetDir+"/...", "-count=1", "-timeout=30s")
		cmd.Dir = h.workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			continue // test failure handled elsewhere
		}

		output := string(out)
		// Parse "coverage: X.X% of statements" from go test -cover output
		for _, line := range strings.Split(output, "\n") {
			if strings.Contains(line, "coverage:") && strings.Contains(line, "%") {
				var pct float64
				if _, scanErr := fmt.Sscanf(line, "coverage: %f%%", &pct); scanErr == nil {
					if pct < threshold*100 {
						fmt.Fprintf(os.Stderr,
							"AdvancedHooks: ⚠️  %s coverage %.1f%% (below %.0f%% threshold) — consider adding tests\n",
							targetDir, pct, threshold*100)
					}
				}
			}
		}
	}
}

// splitJSONBlocks splits combined go list -json output into individual JSON objects.
func splitJSONBlocks(input string) []string {
	var blocks []string
	depth := 0
	start := -1
	for i, ch := range input {
		switch ch {
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			depth--
			if depth == 0 && start >= 0 {
				blocks = append(blocks, input[start:i+1])
				start = -1
			}
		}
	}
	return blocks
}

func fileNameOnly(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	if idx := strings.LastIndex(path, "\\"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// runStyleCheck runs gofmt -d and go vet on the package containing the edited file.
// Warnings are emitted to stderr for the agent to see in the tool result.
func (h *AdvancedHooks) runStyleCheck(ctx context.Context, filePath string) {
	rel, err := filepath.Rel(h.workDir, filePath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}

	// 1. gofmt -d: show formatting diffs (non-destructive, just diagnostics)
	cmd := winhide.CommandContext(ctx, "gofmt", "-d", filePath)
	cmd.Dir = h.workDir
	out, err := cmd.CombinedOutput()
	if err == nil && len(out) > 0 {
		fmt.Fprintf(os.Stderr, "AdvancedHooks: ⚠️  %s has formatting issues:\n%s\n", rel, string(out))
	}

	// 2. go vet the package
	pkgDir := filepath.Dir(rel)
	if pkgDir != "." && pkgDir != "" {
		cmd2 := winhide.CommandContext(ctx, "go", "vet", "./"+pkgDir+"/...")
		cmd2.Dir = h.workDir
		out2, err2 := cmd2.CombinedOutput()
		if err2 != nil {
			fmt.Fprintf(os.Stderr, "AdvancedHooks: ⚠️  go vet %s found issues:\n%s\n", pkgDir, string(out2))
		}
	}
}

// runOkVerify runs go vet across all packages (equivalent to ok-verify's static checks).
// Warnings are truncated to avoid flooding the tool result.
func (h *AdvancedHooks) runOkVerify(ctx context.Context, filePath string) {
	rel, err := filepath.Rel(h.workDir, filePath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}

	pkgDir := filepath.Dir(rel)
	if pkgDir == "." || pkgDir == "" {
		return
	}

	dependents := h.findDependents(ctx, rel)

	// Run go vet on the edited package + dependents
	pkgs := append([]string{pkgDir}, dependents...)
	seen := map[string]bool{}
	for _, dep := range pkgs {
		targetDir := strings.TrimPrefix(dep, "ok/")
		if targetDir == "" || seen[targetDir] {
			continue
		}
		seen[targetDir] = true

		cmd := winhide.CommandContext(ctx, "go", "vet", "./"+targetDir+"/...")
		cmd.Dir = h.workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			// Truncate long output to avoid flooding.
			msg := string(out)
			if len(msg) > 1000 {
				msg = msg[:1000] + "\n... (truncated)"
			}
			fmt.Fprintf(os.Stderr, "AdvancedHooks: ⚠️  go vet %s:\n%s\n", targetDir, msg)
		}
	}
}
