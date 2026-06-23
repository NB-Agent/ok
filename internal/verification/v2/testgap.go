package v2

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ─── Test Gap Detector ────────────────────────────────────────────────────
// Flags Go packages that have source files but zero _test.go files.

type testGapAnalyzer struct{}

func (testGapAnalyzer) Name() string        { return "test-gap" }
func (testGapAnalyzer) Layer() string       { return "architecture" }
func (testGapAnalyzer) Languages() []string { return []string{"go"} }

func (testGapAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	var ff []Finding
	//nolint:errcheck // callback handles err internally
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "testdata" {
			return filepath.SkipDir
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		hasGo := false
		hasTest := false
		goCount := 0
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.HasSuffix(e.Name(), ".go") {
				hasGo = true
				if !strings.HasSuffix(e.Name(), "_test.go") {
					goCount++
				}
			}
			if strings.HasSuffix(e.Name(), "_test.go") {
				hasTest = true
			}
		}
		if hasGo && !hasTest && goCount >= 1 {
			rel, _ := filepath.Rel(root, path)
			ff = append(ff, Finding{
				Analyzer: "test-gap",
				Layer:    "architecture",
				Severity: SevMedium,
				File:     filepath.Join(rel, "(package)"),
				Line:     1,
				Message:  fmt.Sprintf("package has %d Go file(s) but zero test files — add _test.go", goCount),
				Category: "testing",
				Rule:     "TEST-001",
			})
		}
		return nil
	})
	return ff, nil
}
