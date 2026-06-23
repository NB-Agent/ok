package v2

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// ─── Race Condition Detector ──────────────────────────────────────────────
// Finds package-level variables that are exported+mutable without mutex
// protection — common race hazard source.

type raceAnalyzer struct{}

func (raceAnalyzer) Name() string        { return "race-condition" }
func (raceAnalyzer) Layer() string       { return "semantic" }
func (raceAnalyzer) Languages() []string { return []string{"go"} }

func (raceAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	var ff []Finding
	//nolint:errcheck // callback handles err internally
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/vendor/") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		if isTestFile2(f) {
			return nil
		}

		// Collect package-level vars that are exported and mutable (not sync.Once or mutex).
		exportedMutable := 0
		hasMutex := usesSyncMutex(f)
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					if isExported(name.Name) && !isGuardVar(name.Name) {
						exportedMutable++
					}
				}
			}
		}
		if exportedMutable > 1 && !hasMutex {
			ff = append(ff, Finding{
				Analyzer: "race-condition",
				Layer:    "semantic",
				Severity: SevMedium,
				File:     path,
				Line:     1,
				Message:  "package has exported mutable variables without sync.Mutex — potential data race",
				Category: "correctness",
				Rule:     "RACE-001",
			})
		}
		return nil
	})
	return ff, nil
}

func isTestFile2(f *ast.File) bool {
	return strings.HasSuffix(f.Name.Name, "_test")
}

func usesSyncMutex(f *ast.File) bool {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "sync" {
			return true
		}
	}
	return false
}

func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	return name[0] >= 'A' && name[0] <= 'Z'
}

func isGuardVar(name string) bool {
	return strings.HasPrefix(name, "mu") || strings.HasPrefix(name, "lock") ||
		strings.HasPrefix(name, "Once") || strings.HasPrefix(name, "rx_") ||
		strings.HasPrefix(name, "tx_")
}
