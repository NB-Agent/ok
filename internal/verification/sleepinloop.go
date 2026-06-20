package verification

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

var SleepInLoop = &analysis.Analyzer{
	Name: "sleepinloop",
	Doc:  "checks that time.Sleep inside for loops uses a timer instead for efficiency",
	Run:  runSleepInLoop,
}

func runSleepInLoop(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			forStmt, ok := n.(*ast.ForStmt)
			if !ok {
				return true
			}
			ast.Inspect(forStmt.Body, func(n2 ast.Node) bool {
				if _, ok := n2.(*ast.ForStmt); ok {
					return false
				}
				call, ok := n2.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || id.Name != "time" || sel.Sel.Name != "Sleep" {
					return true
				}
				pass.Reportf(call.Pos(), "time.Sleep inside for loop — use time.NewTimer and reset for efficiency")
				return true
			})
			return true
		})
	}
	return nil, nil
}
