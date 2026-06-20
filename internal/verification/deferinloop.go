package verification

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

var DeferInLoop = &analysis.Analyzer{
	Name: "deferinloop",
	Doc:  "checks that defer is not used inside a for loop",
	Run:  runDeferInLoop,
}

func runDeferInLoop(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		// Find all for/range loops first
		ast.Inspect(file, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.ForStmt, *ast.RangeStmt:
				return true // walk into loop bodies
			default:
				return true
			}
		})
		// Find defer statements directly inside loops
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.ForStmt:
				checkDeferInBlock(pass, v.Body)
				return false // don't walk into nested constructs
			case *ast.RangeStmt:
				checkDeferInBlock(pass, v.Body)
				return false
			}
			return true
		})
	}
	return nil, nil
}

func checkDeferInBlock(pass *analysis.Pass, body *ast.BlockStmt) {
	if body == nil {
		return
	}
	for _, stmt := range body.List {
		if ds, ok := stmt.(*ast.DeferStmt); ok {
			pass.Reportf(ds.Pos(), "defer inside for loop — resources accumulate until function returns instead of per-iteration")
		}
		// Also check if the loop body contains nested loops with defers
		if forStmt, ok := stmt.(*ast.ForStmt); ok {
			checkDeferInBlock(pass, forStmt.Body)
		}
		if rangeStmt, ok := stmt.(*ast.RangeStmt); ok {
			checkDeferInBlock(pass, rangeStmt.Body)
		}
	}
}
