package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/winhide"
)

type healthCheckAnalyzer struct{}

func (healthCheckAnalyzer) Name() string        { return "health-check" }
func (healthCheckAnalyzer) Layer() string       { return "architecture" }
func (healthCheckAnalyzer) Languages() []string { return []string{"*"} }

func (a healthCheckAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	var ff []Finding
	langs := DetectLanguages(root)
	ff = append(ff, a.runGoBuild(ctx, root, langs)...)
	ff = append(ff, a.runGoTest(ctx, root, langs)...)
	ff = append(ff, a.checkDocCoverage(ctx, root, langs)...)
	ff = append(ff, a.checkFlakyPatterns(ctx, root, langs)...)
	ff = append(ff, a.checkDeadExport(ctx, root, langs)...)
	ff = append(ff, a.checkContributors(ctx, root, langs)...)
	ff = append(ff, a.checkDepFreshness(ctx, root, langs)...)
	return ff, nil
}

func (a healthCheckAnalyzer) runGoBuild(ctx context.Context, root string, langs []string) []Finding {
	if !contains(langs, "go") {
		return nil
	}
	out, err := runCmd(ctx, root, "go", "build", "./...")
	if err == nil {
		return nil
	}
	return []Finding{{Analyzer: "health-check", Layer: "architecture", Severity: SevHigh,
		File: "(root)", Line: 1, Message: "go build failed: " + truncateString(out, 200),
		Category: "correctness", Rule: "HEALTH-B001"}}
}

func (a healthCheckAnalyzer) runGoTest(ctx context.Context, root string, langs []string) []Finding {
	if !contains(langs, "go") {
		return nil
	}
	out, err := runCmd(ctx, root, "go", "test", "-count=1", "./...")
	if err == nil {
		return nil
	}
	return []Finding{{Analyzer: "health-check", Layer: "architecture", Severity: SevHigh,
		File: "(root)", Line: 1, Message: "go test failed: " + truncateString(out, 200),
		Category: "correctness", Rule: "HEALTH-T001"}}
}

func (a healthCheckAnalyzer) checkDocCoverage(_ context.Context, root string, langs []string) []Finding {
	if !contains(langs, "go") {
		return nil
	}
	count := 0
	//nolint:errcheck // callback handles err internally
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/vendor/") {
			return nil
		}
		src, err := readFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(src), "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "type ") {
				if i == 0 || !strings.HasPrefix(strings.TrimSpace(lines[i-1]), "//") {
					for _, w := range strings.Fields(trimmed) {
						if len(w) > 0 && w[0] >= 'A' && w[0] <= 'Z' {
							count++
						}
						break
					}
				}
			}
		}
		return nil
	})
	if count > 0 {
		return []Finding{{Analyzer: "health-check", Layer: "architecture", Severity: SevLow,
			File: "(root)", Line: 1,
			Message:  fmt.Sprintf("%d exported symbols missing doc comments", count),
			Category: "maintenance", Rule: "HEALTH-D001"}}
	}
	return nil
}

func (a healthCheckAnalyzer) checkFlakyPatterns(_ context.Context, root string, langs []string) []Finding {
	var ff []Finding
	if !contains(langs, "go") {
		return ff
	}
	//nolint:errcheck // callback handles err internally
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/vendor/") {
			return nil
		}
		src, err := readFile(path)
		if err != nil {
			return nil
		}
		content := string(src)
		if strings.Contains(content, "time.Sleep") {
			ff = append(ff, Finding{
				Analyzer: "health-check", Layer: "architecture", Severity: SevLow,
				File: path, Line: 1, Message: "time.Sleep in test — use eventually/retry",
				Category: "maintenance", Rule: "HEALTH-F001",
			})
		}
		if strings.Contains(content, "func Test") && !strings.Contains(content, "assert.") &&
			!strings.Contains(content, "require.") && !strings.Contains(content, "t.Error") {
			ff = append(ff, Finding{
				Analyzer: "health-check", Layer: "architecture", Severity: SevLow,
				File: path, Line: 1, Message: "test file has no assertions",
				Category: "maintenance", Rule: "HEALTH-F002",
			})
		}
		return nil
	})
	return ff
}

func (a healthCheckAnalyzer) checkDeadExport(_ context.Context, root string, langs []string) []Finding {
	if !contains(langs, "go") {
		return nil
	}
	var exports []struct{ name, file string }
	//nolint:errcheck // callback handles err internally
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/vendor/") {
			return nil
		}
		src, err := readFile(path)
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			for _, prefix := range []string{"func ", "type ", "var ", "const "} {
				if strings.HasPrefix(trimmed, prefix) {
					rest := strings.TrimSpace(trimmed[len(prefix):])
					parts := strings.Fields(rest)
					if len(parts) > 0 && len(parts[0]) > 0 && parts[0][0] >= 'A' && parts[0][0] <= 'Z' {
						exports = append(exports, struct{ name, file string }{parts[0], path})
					}
				}
			}
		}
		return nil
	})
	var ff []Finding
	for _, e := range exports {
		if e.name == "main" || e.name == "init" {
			continue
		}
		if !isUsedElsewhere(root, e.name, e.file) {
			ff = append(ff, Finding{
				Analyzer: "health-check", Layer: "architecture", Severity: SevLow,
				File: e.file, Line: 1,
				Message:  fmt.Sprintf("exported %s may be dead code", e.name),
				Category: "dead-code", Rule: "HEALTH-X001",
			})
		}
	}
	return ff
}

func isUsedElsewhere(root, sym, excludeFile string) bool {
	cmd := winhide.Command("grep", "-rl", "--include=*.go", sym, root)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, f := range strings.Fields(string(out)) {
		if f != excludeFile {
			return true
		}
	}
	return false
}

func (a healthCheckAnalyzer) checkContributors(ctx context.Context, root string, _ []string) []Finding {
	if !which("git") {
		return nil
	}
	out, err := runCmd(ctx, root, "git", "shortlog", "-sn", "--all", "--no-merges", "HEAD")
	if err != nil || out == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) <= 1 {
		return nil
	}
	var firstN, total int
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			n := 0
			fmt.Sscanf(parts[0], "%d", &n)
			total += n
			if firstN == 0 {
				firstN = n
			}
		}
	}
	if total > 0 && firstN*100/total > 60 {
		return []Finding{{Analyzer: "health-check", Layer: "architecture", Severity: SevMedium,
			File: "(root)", Line: 1,
			Message:  fmt.Sprintf("bus factor = 1: top contributor has %d%% of commits", firstN*100/total),
			Category: "architecture", Rule: "HEALTH-BF001"}}
	}
	return nil
}

func (a healthCheckAnalyzer) checkDepFreshness(ctx context.Context, root string, langs []string) []Finding {
	if !contains(langs, "go") || !which("go") {
		return nil
	}
	out, err := runCmd(ctx, root, "go", "list", "-m", "-u", "-json", "all")
	if err != nil {
		return nil
	}
	type modJSON struct {
		Path    string `json:"Path"`
		Version string `json:"Version"`
		Update  *struct {
			Version string `json:"Version"`
		} `json:"Update,omitempty"`
	}
	var ff []Finding
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var m modJSON
		if err := json.Unmarshal([]byte(line), &m); err != nil || m.Update == nil {
			continue
		}
		ff = append(ff, Finding{
			Analyzer: "health-check", Layer: "architecture", Severity: SevLow,
			File: "go.mod", Line: 1,
			Message:  fmt.Sprintf("dep %s@%s can update to %s", m.Path, m.Version, m.Update.Version),
			Category: "dependency", Rule: "HEALTH-DEP001",
			Fix: fmt.Sprintf("go get %s@%s", m.Path, m.Update.Version),
		})
	}
	return ff
}
