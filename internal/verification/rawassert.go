package verification

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

var RawAssert = &analysis.Analyzer{
	Name: "rawassert",
	Doc:  "checks that type assertions use the comma-ok pattern to prevent panic on failure",
	Run:  runRawAssert,
}

func runRawAssert(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			tae, ok := n.(*ast.TypeAssertExpr)
			if !ok {
				return true
			}
			if tae.Type == nil {
				return true
			}
			// Check if this assertion is in an assignment (comma-ok pattern)
			parentIsAssign := false
			ast.Inspect(file, func(parent ast.Node) bool {
				as, ok := parent.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, rhs := range as.Rhs {
					if rhs == tae {
						parentIsAssign = len(as.Lhs) == 2
						return false
					}
				}
				return true
			})
			if !parentIsAssign {
				pass.Reportf(tae.Pos(), "type assertion without comma-ok pattern — will panic on mismatch")
			}
			return true
		})
	}
	return nil, nil
}
