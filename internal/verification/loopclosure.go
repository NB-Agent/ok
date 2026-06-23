package verification

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var LoopClosure = &analysis.Analyzer{
	Name: "loopclosure",
	Doc:  "checks that goroutines or defer statements inside loops capture loop variables correctly",
	Run:  runLoopClosure,
}

func runLoopClosure(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			rangeStmt, ok := n.(*ast.RangeStmt)
			if !ok {
				return true
			}
			// Get the loop variable
			var loopVar *ast.Ident
			if kv, ok := rangeStmt.Value.(*ast.Ident); ok {
				loopVar = kv
			} else if kv, ok := rangeStmt.Key.(*ast.Ident); ok {
				loopVar = kv
			} else {
				return true
			}

			// Look inside the loop body for go/defer that references loopVar
			ast.Inspect(rangeStmt.Body, func(n2 ast.Node) bool {
				switch n2.(type) {
				case *ast.GoStmt, *ast.DeferStmt:
					// Check if this go/defer captures the loop variable
					captures := false
					ast.Inspect(n2, func(n3 ast.Node) bool {
						if id, ok := n3.(*ast.Ident); ok && id.Name == loopVar.Name {
							// Make sure it's the same object (not a shadow)
							obj := pass.TypesInfo.ObjectOf(id)
							if obj != nil {
								if v, ok := obj.(*types.Var); ok && v.Parent() == pass.TypesInfo.Scopes[rangeStmt] {
									captures = true
									return false
								}
							}
						}
						return true
					})
					if captures {
						pass.Reportf(n2.Pos(), "goroutine/defer captures loop variable %s — may see wrong value; copy inside loop", loopVar.Name)
					}
				}
				return true
			})
			return true
		})
	}
	return nil, nil
}
