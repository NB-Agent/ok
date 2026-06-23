package verification

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var NilMapWrite = &analysis.Analyzer{
	Name: "nilmapwrite",
	Doc:  "checks for possible nil map writes that would panic",
	Run:  runNilMapWrite,
}

func runNilMapWrite(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			// Look for `x = nil` where x is a map
			if len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				return true
			}
			ident, ok := as.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			rhs, ok := as.Rhs[0].(*ast.Ident)
			if !ok || rhs.Name != "nil" {
				return true
			}
			// Check type: is it a map?
			obj := pass.TypesInfo.ObjectOf(ident)
			if obj == nil {
				return true
			}
			if _, ok := obj.Type().Underlying().(*types.Map); ok {
				pass.Reportf(as.Pos(), "%s is a map type being set to nil — concurrent writes will panic; use clear() or make() instead", ident.Name)
			}
			return true
		})
	}
	return nil, nil
}
