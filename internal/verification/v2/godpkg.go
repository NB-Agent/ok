package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NB-Agent/ok/internal/winhide"
)

// ─── God Package Detector ─────────────────────────────────────────────────
// Flags packages with >30 direct imports or imported by >80% of the project.

type godPkgAnalyzer struct{}

func (godPkgAnalyzer) Name() string        { return "god-package" }
func (godPkgAnalyzer) Layer() string       { return "architecture" }
func (godPkgAnalyzer) Languages() []string { return []string{"go"} }

// pkgInfo holds parsed go list -json output for one package.
type pkgInfo struct {
	ImportPath string   `json:"ImportPath"`
	Imports    []string `json:"Imports"`
}

func (godPkgAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	out, err := runCmd(ctx, root, "go", "list", "-json", "./...")
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	var ff []Finding
	modulePrefix := detectModule(root)

	// Parse JSON records (one per line — go list -json prints one JSON object per line).
	dec := json.NewDecoder(strings.NewReader(out))
	var pkgs []pkgInfo
	for dec.More() {
		var p pkgInfo
		if err := dec.Decode(&p); err != nil {
			break
		}
		pkgs = append(pkgs, p)
	}

	// Count reverse dependencies: for each package, count how many others import it.
	importedBy := make(map[string]int, len(pkgs))
	for _, p := range pkgs {
		// Ensure every package is in the map (0 imports).
		if _, ok := importedBy[p.ImportPath]; !ok {
			importedBy[p.ImportPath] = 0
		}
		for _, imp := range p.Imports {
			importedBy[imp]++
		}
	}

	for _, p := range pkgs {
		dir := strings.TrimPrefix(p.ImportPath, modulePrefix)
		dir = strings.TrimPrefix(dir, "/")
		if dir == "" {
			dir = "(root)"
		}

		if len(p.Imports) > 30 {
			ff = append(ff, Finding{
				Analyzer: "god-package",
				Layer:    "architecture",
				Severity: SevMedium,
				File:     filepath.Join(dir, "(package)"),
				Line:     1,
				Message:  "god package with " + strconv.Itoa(len(p.Imports)) + " imports — split into smaller packages",
				Category: "architecture",
				Rule:     "ARCH-001",
			})
		}

		// Hub package check: if this internal package is imported by >80% of project packages.
		if strings.HasPrefix(p.ImportPath, modulePrefix) {
			importers := importedBy[p.ImportPath]
			if importers > 20 && len(pkgs) > 0 {
				pct := float64(importers) / float64(len(pkgs)) * 100
				if pct > 80 {
					ff = append(ff, Finding{
						Analyzer: "god-package",
						Layer:    "architecture",
						Severity: SevHigh,
						File:     filepath.Join(dir, "(package)"),
						Line:     1,
						Message:  "hub package imported by " + strconv.Itoa(importers) + " packages (" + strconv.Itoa(int(pct)) + "% of project) — extract interfaces",
						Category: "architecture",
						Rule:     "ARCH-002",
					})
				}
			}
		}
	}
	return ff, nil
}

func detectModule(root string) string {
	cmd := winhide.Command("go", "list", "-m")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
