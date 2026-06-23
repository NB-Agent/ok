package verification

import (
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/analysis"
)

type closeCall struct {
	pos  token.Pos
	name string
}

var DoubleClose = &analysis.Analyzer{
	Name: "doubleclose",
	Doc:  "checks for channels that may be closed more than once",
	Run:  runDoubleClose,
}

func runDoubleClose(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		closeCalls := make(map[string][]closeCall)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name != "close" {
				return true
			}
			chName := extractIdent(sel.X)
			if chName == "?" || chName == "" {
				return true
			}
			closeCalls[chName] = append(closeCalls[chName], closeCall{
				pos:  call.Pos(),
				name: chName,
			})
			return true
		})
		for _, calls := range closeCalls {
			if len(calls) > 1 {
				for _, c := range calls {
					pass.Reportf(c.pos, "channel %s may be closed multiple times — use sync.Once or check closed status", c.name)
				}
			}
		}
	}
	return nil, nil
}
