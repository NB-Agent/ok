package verification

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

var SwitchDefault = &analysis.Analyzer{
	Name: "switchdefault",
	Doc:  "checks that switch statements have a default branch to handle unexpected values",
	Run:  runSwitchDefault,
}

func runSwitchDefault(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			ss, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			hasDefault := false
			for _, stmt := range ss.Body.List {
				if cl, ok := stmt.(*ast.CaseClause); ok && cl.List == nil {
					hasDefault = true
					break
				}
			}
			if !hasDefault {
				pass.Reportf(ss.Pos(), "switch statement without default branch — unexpected values are silently ignored")
			}
			return true
		})
	}
	return nil, nil
}
