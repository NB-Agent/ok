package verification

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var SprintfHex = &analysis.Analyzer{
	Name: "sprintfhex",
	Doc:  "checks that fmt.Sprintf with %%x is replaced by the faster hex.EncodeToString",
	Run:  runSprintfHex,
}

func runSprintfHex(pass *analysis.Pass) (interface{}, error) {
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
			if !ok || id.Name != "fmt" || sel.Sel.Name != "Sprintf" {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return true
			}
			val := lit.Value
			if strings.Contains(val, `%x`) || strings.Contains(val, `%X`) {
				pass.Reportf(call.Pos(), "fmt.Sprintf with %s — use encoding/hex.EncodeToString for better performance", val)
			}
			return true
		})
	}
	return nil, nil
}
