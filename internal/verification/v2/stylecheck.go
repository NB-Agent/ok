package v2

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type styleCheckAnalyzer struct{}

func (styleCheckAnalyzer) Name() string  { return "style-check" }
func (styleCheckAnalyzer) Layer() string { return "semantic" }
func (styleCheckAnalyzer) Languages() []string {
	return []string{"go", "python", "javascript", "typescript", "shell", "rust"}
}

func (a styleCheckAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	var ff []Finding
	langs := DetectLanguages(root)
	ff = append(ff, a.runGofmt(ctx, root, langs)...)
	ff = append(ff, a.runPrettier(ctx, root, langs)...)
	ff = append(ff, a.runBlack(ctx, root, langs)...)
	ff = append(ff, a.runRustfmt(ctx, root, langs)...)
	ff = append(ff, a.runShfmt(ctx, root, langs)...)
	ff = append(ff, a.checkNaming(ctx, root, langs)...)
	ff = append(ff, a.checkErrorHandling(ctx, root, langs)...)
	ff = append(ff, a.checkImportOrder(ctx, root, langs)...)
	return ff, nil
}

func (a styleCheckAnalyzer) runGofmt(ctx context.Context, root string, langs []string) []Finding {
	if !contains(langs, "go") || !which("gofmt") {
		return nil
	}
	out, err := runCmd(ctx, root, "gofmt", "-d", "-e", ".")
	if err != nil {
		_ = err
	}
	if out == "" {
		return nil
	}
	return []Finding{{Analyzer: "style-check", Layer: "semantic", Severity: SevLow,
		File: "(root)", Line: 1, Message: "gofmt finds formatting issues",
		Category: "style", Rule: "STYLE-001", Fix: truncateString(out, 500)}}
}

func (a styleCheckAnalyzer) runPrettier(ctx context.Context, root string, langs []string) []Finding {
	if !containsAny(langs, []string{"javascript", "typescript"}) {
		return nil
	}
	if !which("npx") && !which("prettier") {
		return nil
	}
	cmd, args := "prettier", []string{"--check", "."}
	if !which("prettier") {
		cmd, args = "npx", append([]string{"prettier"}, args...)
	}
	out, err := runCmd(ctx, root, cmd, args...)
	if err == nil || out == "" {
		return nil
	}
	return []Finding{{Analyzer: "style-check", Layer: "semantic", Severity: SevLow,
		File: "(root)", Line: 1, Message: "Prettier finds formatting issues",
		Category: "style", Rule: "STYLE-002"}}
}

func (a styleCheckAnalyzer) runBlack(ctx context.Context, root string, langs []string) []Finding {
	if !contains(langs, "python") || !which("black") {
		return nil
	}
	out, err := runCmd(ctx, root, "black", "--check", "--diff", ".")
	if err == nil || out == "" {
		return nil
	}
	return []Finding{{Analyzer: "style-check", Layer: "semantic", Severity: SevLow,
		File: "(root)", Line: 1, Message: "Black finds formatting issues",
		Category: "style", Rule: "STYLE-003"}}
}

func (a styleCheckAnalyzer) runRustfmt(ctx context.Context, root string, langs []string) []Finding {
	if !contains(langs, "rust") || !which("rustfmt") {
		return nil
	}
	out, err := runCmd(ctx, root, "cargo", "fmt", "--check")
	if err == nil || out == "" {
		return nil
	}
	return []Finding{{Analyzer: "style-check", Layer: "semantic", Severity: SevLow,
		File: "(root)", Line: 1, Message: "rustfmt finds issues",
		Category: "style", Rule: "STYLE-004"}}
}

func (a styleCheckAnalyzer) runShfmt(ctx context.Context, root string, langs []string) []Finding {
	if !contains(langs, "shell") || !which("shfmt") {
		return nil
	}
	out, err := runCmd(ctx, root, "shfmt", "-d", ".")
	if err == nil || out == "" {
		return nil
	}
	return []Finding{{Analyzer: "style-check", Layer: "semantic", Severity: SevLow,
		File: "(root)", Line: 1, Message: "shfmt finds issues",
		Category: "style", Rule: "STYLE-005"}}
}

func (a styleCheckAnalyzer) checkNaming(_ context.Context, root string, langs []string) []Finding {
	if !contains(langs, "go") {
		return nil
	}
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
					if !name.IsExported() {
						continue
					}
					for _, bad := range []string{"Url", "Id", "Json", "Http", "Ip", "Html"} {
						if strings.Contains(name.Name, bad) {
							ff = append(ff, Finding{
								Analyzer: "style-check", Layer: "semantic", Severity: SevLow,
								File: path, Line: fset.Position(name.Pos()).Line,
								Column:   fset.Position(name.Pos()).Column,
								Message:  fmt.Sprintf("var %s uses non-standard casing — should be all-caps", name.Name),
								Category: "style", Rule: "STYLE-N001",
							})
						}
					}
				}
			}
		}
		return nil
	})
	return ff
}

func (a styleCheckAnalyzer) checkErrorHandling(_ context.Context, root string, langs []string) []Finding {
	if !contains(langs, "go") {
		return nil
	}
	var ff []Finding
	//nolint:errcheck // callback handles err internally
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.Contains(path, "/vendor/") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || as.Tok != token.DEFINE {
				return true
			}
			for _, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == "_" {
					ff = append(ff, Finding{
						Analyzer: "style-check", Layer: "semantic", Severity: SevMedium,
						File: path, Line: fset.Position(as.Pos()).Line,
						Column:   fset.Position(as.Pos()).Column,
						Message:  "error ignored with '_'",
						Category: "correctness", Rule: "STYLE-E001",
					})
				}
			}
			return true
		})
		return nil
	})
	return ff
}

func (a styleCheckAnalyzer) checkImportOrder(_ context.Context, root string, langs []string) []Finding {
	if !contains(langs, "go") {
		return nil
	}
	var ff []Finding
	mod := moduleName(root)
	//nolint:errcheck // callback handles err internally
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.Contains(path, "/vendor/") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil || len(f.Imports) < 2 {
			return nil
		}
		groups := make([]int, len(f.Imports))
		for i, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if isStdLib(p) {
				groups[i] = 0
			} else if mod != "" && strings.HasPrefix(p, mod) {
				groups[i] = 2
			} else {
				groups[i] = 1
			}
			if i > 0 && groups[i] < groups[i-1] {
				ff = append(ff, Finding{
					Analyzer: "style-check", Layer: "semantic", Severity: SevLow,
					File: path, Line: fset.Position(imp.Pos()).Line,
					Column:   fset.Position(imp.Pos()).Column,
					Message:  fmt.Sprintf("import %q out of order", p),
					Category: "style", Rule: "STYLE-I001",
				})
			}
		}
		return nil
	})
	return ff
}
