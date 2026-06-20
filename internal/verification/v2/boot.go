package v2

import (
	"context"
	"os"
	"path/filepath"
)

// ─── Registry Boot ────────────────────────────────────────────────────────

// NewDefaultRegistry creates a Registry pre-loaded with all analyzers.
// External adapters are added based on tool availability; self-built
// analyzers are always added for the languages they support.
func NewDefaultRegistry() *Registry {
	r := NewRegistry()

	// Layer 1: External tool adapters (added if tool is in PATH).
	r.Add(golangciLintAnalyzer{})
	r.Add(semgrepAnalyzer{})
	r.Add(ruffAnalyzer{})
	r.Add(eslintAnalyzer{})
	r.Add(shellcheckAnalyzer{})

	// Layer 2: Self-built semantic analyzers.
	r.Add(sqliAnalyzer{})
	r.Add(cryptoAnalyzer{})
	r.Add(raceAnalyzer{})
	r.Add(leakAnalyzer{})

	// Layer 3: Architecture scanners.
	r.Add(godPkgAnalyzer{})
	r.Add(testGapAnalyzer{})

	// Converted from built-in analysis tools.
	r.Add(styleCheckAnalyzer{})
	r.Add(vulnCheckAnalyzer{})
	r.Add(healthCheckAnalyzer{})

	return r
}

// ─── Language Detection ───────────────────────────────────────────────────

// DetectLanguages guesses the primary languages in a project directory
// by checking for marker files.
func DetectLanguages(root string) []string {
	var langs []string
	seen := map[string]bool{}

	// Go
	if exists(filepath.Join(root, "go.mod")) || exists(filepath.Join(root, "go.sum")) {
		langs = append(langs, "go")
		seen["go"] = true
	}

	// Python
	if exists(filepath.Join(root, "pyproject.toml")) || exists(filepath.Join(root, "setup.py")) ||
		exists(filepath.Join(root, "requirements.txt")) || hasExt(root, ".py") {
		langs = append(langs, "python")
		seen["python"] = true
	}

	// JavaScript / TypeScript
	if exists(filepath.Join(root, "package.json")) {
		langs = append(langs, "javascript")
		seen["javascript"] = true
		if exists(filepath.Join(root, "tsconfig.json")) || hasExt(root, ".ts") || hasExt(root, ".tsx") {
			langs = append(langs, "typescript")
			seen["typescript"] = true
		}
	}

	// Shell
	if hasExt(root, ".sh") || exists(filepath.Join(root, "Makefile")) {
		langs = append(langs, "shell")
		seen["shell"] = true
	}

	// Rust
	if exists(filepath.Join(root, "Cargo.toml")) || exists(filepath.Join(root, "Cargo.lock")) {
		langs = append(langs, "rust")
		seen["rust"] = true
	}

	return langs
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasExt(root, ext string) bool {
	found := false
	//nolint:errcheck // callback handles err internally
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if found || err != nil || info.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ext {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// ─── Convenience ──────────────────────────────────────────────────────────

// Scan runs all analyzers against root, auto-detecting languages.
// Returns a ready-to-display Report.
func Scan(ctx context.Context, root string) (*Report, error) {
	r := NewDefaultRegistry()
	langs := DetectLanguages(root)

	var all []Finding
	for _, lang := range langs {
		ff := r.RunAll(ctx, root, lang)
		all = append(all, ff...)
	}

	// Also run language-agnostic analyzers ("*").
	ff := r.RunAll(ctx, root, "*")
	all = append(all, ff...)

	return &Report{
		Findings: all,
		Summary:  BuildSummary(all),
	}, nil
}
