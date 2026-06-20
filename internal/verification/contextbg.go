package verification

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

var ContextBg = &analysis.Analyzer{
	Name: "contextbg",
	Doc:  "checks that context.Background() is not used where a derived context is available",
	Run:  runContextBg,
}

func runContextBg(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != "context" || sel.Sel.Name != "Background" {
				return true
			}
			if len(call.Args) != 0 {
				return true
			}
			pass.Reportf(call.Pos(), "context.Background() should not be used in library packages — use a derived context from the caller")
			return true
		})
	}
	return nil, nil
}
