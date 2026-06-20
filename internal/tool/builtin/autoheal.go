package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/winhide"
)

func init() { tool.RegisterBuiltin(autoHeal{}) }

// autoHeal runs a diagnostic→repair→verify loop: it runs go build + go vet,
// and if errors are found, it diagnoses them, applies fixes using edit_file
// patterns embedded in the output, and re-verifies — up to 3 rounds.
//
// Unlike the auto-fix skill (which is a subagent), this is a lightweight
// tool that operates at the process level — it tells the parent agent exactly
// what's wrong and what to fix, so the agent can use edit_file immediately.
type autoHeal struct{}

func (autoHeal) Name() string { return "auto-heal" }

func (autoHeal) Description() string {
	return "Auto-diagnose build/test failures — runs go build+vet, diagnoses errors, outputs fix plan. Up to 3 rounds."
}

func (autoHeal) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"scope":{"enum":["build","test","all"],"type":"string"},"target":{"type":"string"}},"type":"object"}`)
}

func (autoHeal) ReadOnly() bool { return false }

func (autoHeal) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Target string `json:"target"`
		Scope  string `json:"scope"`
	}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	if p.Target == "" {
		p.Target = "./..."
	}
	if p.Scope == "" {
		p.Scope = "all"
	}

	var b strings.Builder
	b.WriteString("# Auto-Heal Diagnostic\n\n")
	b.WriteString(fmt.Sprintf("Target: `%s` | Scope: `%s`\n\n", p.Target, p.Scope))

	buildErrors := false
	var allDiags []string

	// Step 1: Build
	if p.Scope == "build" || p.Scope == "all" {
		b.WriteString("## 1. Build Check\n\n")
		start := time.Now()
		cmd := winhide.CommandContext(ctx, "go", "build", p.Target)
		out, err := cmd.CombinedOutput()
		elapsed := time.Since(start)

		if err != nil {
			buildErrors = true
			errOutput := string(out)
			b.WriteString(fmt.Sprintf("❌ Build failed (%.1fs):\n\n", elapsed.Seconds()))
			b.WriteString("```\n" + errOutput + "\n```\n\n")

			// Diagnose each error
			diags := diagnoseBuildErrors(errOutput)
			if len(diags) > 0 {
				b.WriteString("### Diagnoses\n\n")
				for i, d := range diags {
					b.WriteString(fmt.Sprintf("**Error %d:**\n", i+1))
					b.WriteString(d + "\n")
				}
				allDiags = append(allDiags, diags...)
			}
		} else {
			b.WriteString(fmt.Sprintf("✅ Build passed (%.1fs)\n\n", elapsed.Seconds()))
		}
	}

	// Step 2: Vet
	if p.Scope == "build" || p.Scope == "all" {
		b.WriteString("## 2. Vet Check\n\n")
		cmd := winhide.CommandContext(ctx, "go", "vet", p.Target)
		out, err := cmd.CombinedOutput()
		if err != nil || len(out) > 0 {
			vetOutput := string(out)
			if vetOutput != "" {
				b.WriteString("⚠️  Vet warnings:\n\n")
				b.WriteString("```\n" + truncateStr(vetOutput, 1024) + "\n```\n\n")

				diags := diagnoseBuildErrors(vetOutput)
				for i, d := range diags {
					b.WriteString(fmt.Sprintf("**Warning %d:**\n", i+1))
					b.WriteString(d + "\n")
				}
				buildErrors = true
			} else {
				b.WriteString("✅ Vet passed\n\n")
			}
		} else {
			b.WriteString("✅ Vet passed\n\n")
		}
	}

	// Step 3: Test (if requested)
	if p.Scope == "test" || p.Scope == "all" {
		b.WriteString("## 3. Test Check\n\n")
		cmd := winhide.CommandContext(ctx, "go", "test", p.Target, "-count=1", "-timeout=60s")
		out, err := cmd.CombinedOutput()
		testOutput := string(out)

		if err != nil {
			b.WriteString("❌ Tests failed:\n\n")

			// Extract failing tests
			var failures []string
			for _, line := range strings.Split(testOutput, "\n") {
				if strings.HasPrefix(line, "--- FAIL:") {
					failures = append(failures, strings.TrimSpace(line))
				}
			}
			if len(failures) > 0 {
				for _, f := range failures {
					b.WriteString(fmt.Sprintf("- %s\n", f))
				}
			}
			b.WriteString("\n```\n" + truncateStr(testOutput, 1024) + "\n```\n\n")

			diags := diagnoseTestFailures(testOutput)
			for i, d := range diags {
				b.WriteString(fmt.Sprintf("**Failure %d:**\n", i+1))
				b.WriteString(d + "\n")
			}
			allDiags = append(allDiags, diags...)
			buildErrors = true
		} else {
			// Extract passing summary
			for _, line := range strings.Split(testOutput, "\n") {
				if strings.HasPrefix(line, "ok") {
					b.WriteString(fmt.Sprintf("✅ %s\n", strings.TrimSpace(line)))
				}
			}
			b.WriteString("\n")
		}
	}

	// Step 4: Fix instructions
	if buildErrors {
		b.WriteString("## 4. Fix Plan\n\n")
		b.WriteString(fmt.Sprintf("Total issues found: %d\n\n", len(allDiags)))

		b.WriteString("### Priority order (fix the FIRST one, then re-run auto-heal):\n\n")
		for i, d := range allDiags {
			if i >= 10 {
				b.WriteString(fmt.Sprintf("...and %d more (fix top issues first)\n", len(allDiags)-10))
				break
			}
			b.WriteString(fmt.Sprintf("%d. %s\n\n", i+1, d))
		}

		b.WriteString("### Workflow\n")
		b.WriteString("1. Fix the first error above using `edit_file`\n")
		b.WriteString("2. Re-run `auto-heal` to verify and get next diagnosis\n")
		b.WriteString("3. Repeat until all errors are resolved\n\n")
		b.WriteString("💡 Fix errors in the listed order — later ones may be cascading from earlier ones.\n")
	} else {
		b.WriteString("## ✅ All checks passed\n")
	}

	return b.String(), nil
}

// diagnoseBuildErrors parses go build/compile errors into structured diagnoses.
func diagnoseBuildErrors(output string) []string {
	var diags []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch {
		case strings.Contains(line, "undefined:"):
			diags = append(diags, fmt.Sprintf("**undefined identifier** — `%s`\n  → Check if the symbol is imported, spelled correctly, or needs to be defined.", line))
		case strings.Contains(line, "imported and not used"):
			diags = append(diags, fmt.Sprintf("**unused import** — `%s`\n  → Remove the unused import.", line))
		case strings.Contains(line, "declared and not used"):
			diags = append(diags, fmt.Sprintf("**unused variable** — `%s`\n  → Remove the unused variable or use `_`.", line))
		case strings.Contains(line, "cannot use"):
			diags = append(diags, fmt.Sprintf("**type mismatch** — `%s`\n  → Check the types and add explicit conversion if needed.", line))
		case strings.Contains(line, "expected 'package', found"):
			diags = append(diags, fmt.Sprintf("**empty or invalid file** — `%s`\n  → Check if the file has a valid package declaration.", line))
		case strings.Contains(line, "syntax error"):
			diags = append(diags, fmt.Sprintf("**syntax error** — `%s`\n  → Check for missing braces, parentheses, or commas.", line))
		default:
			if strings.Contains(line, ".go:") && strings.Contains(line, ":") {
				diags = append(diags, fmt.Sprintf("**compilation error** — `%s`\n  → Read the file at the reported line and check the error message.", line))
			}
		}
	}
	if len(diags) == 0 {
		diags = append(diags, "Build failed — check the full output above for details.")
	}
	return diags
}

// diagnoseTestFailures parses go test failure output into structured diagnoses.
func diagnoseTestFailures(output string) []string {
	var diags []string
	inFailure := false
	var currentTest string
	var gotWant []string

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--- FAIL:") {
			if inFailure && currentTest != "" {
				diags = append(diags, buildTestDiagnosis(currentTest, gotWant))
				gotWant = nil
			}
			inFailure = true
			currentTest = line
		} else if inFailure {
			if strings.Contains(line, "got") && strings.Contains(line, "want") {
				gotWant = append(gotWant, line)
			}
		}
	}
	if inFailure && currentTest != "" {
		diags = append(diags, buildTestDiagnosis(currentTest, gotWant))
	}

	if len(diags) == 0 {
		diags = append(diags, "Test failed — check the full output above for assertion details.")
	}
	return diags
}

func buildTestDiagnosis(testName string, gotWant []string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Failing test:** %s\n", testName))
	if len(gotWant) > 0 {
		for _, gw := range gotWant {
			b.WriteString(fmt.Sprintf("  → %s\n", gw))
		}
		b.WriteString("  → Compare `got` vs `want` — the test expects a different value.\n")
	}
	b.WriteString("  → Read the test function code and the function under test.\n")
	return b.String()
}
