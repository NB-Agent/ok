package verification

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

var CloseCheck = &analysis.Analyzer{
	Name: "closecheck",
	Doc:  "checks that deferred Close() calls check their error return to prevent silent data loss",
	Run:  runCloseCheck,
}

func runCloseCheck(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			ds, ok := n.(*ast.DeferStmt)
			if !ok {
				return true
			}
			call, ok := ds.Call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if call.Sel.Name == "Close" {
				pass.Reportf(ds.Pos(), "defer %s.Close() without error check — may silently lose data",
					extractIdent(call.X))
			}
			return true
		})
	}
	return nil, nil
}
