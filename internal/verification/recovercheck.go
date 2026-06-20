package verification

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

var RecoverCheck = &analysis.Analyzer{
	Name: "recovercheck",
	Doc:  "checks that every goroutine has a defer-recover to prevent process crash on panic",
	Run:  runRecoverCheck,
}

func runRecoverCheck(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			goStmt, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			funcLit, ok := goStmt.Call.Fun.(*ast.FuncLit)
			if !ok {
				return true
			}
			if !hasDeferRecover(funcLit.Body) {
				pass.Reportf(goStmt.Pos(), "goroutine must have defer-recover to prevent process crash on panic")
			}
			return true
		})
	}
	return nil, nil
}
