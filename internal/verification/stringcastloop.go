package verification

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

var StringCastLoop = &analysis.Analyzer{
	Name: "stringcastloop",
	Doc:  "checks for repeated []byte(string) conversions inside loops that could be hoisted",
	Run:  runStringCastLoop,
}

func runStringCastLoop(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			_, ok := n.(*ast.RangeStmt)
			if !ok {
				return true
			}
			// Find []byte(string(someVar)) inside the loop
			ast.Inspect(n, func(n2 ast.Node) bool {
				call, ok := n2.(*ast.CallExpr)
				if !ok {
					return true
				}
				// Check for []byte(...) conversion
				arr, ok := call.Fun.(*ast.ArrayType)
				if !ok || arr.Len != nil || arr.Elt == nil {
					return true
				}
				elt, ok := arr.Elt.(*ast.Ident)
				if !ok || elt.Name != "byte" {
					return true
				}
				if len(call.Args) != 1 {
					return true
				}
				// Check argument is a string conversion or ident
				if id, ok := call.Args[0].(*ast.Ident); ok {
					pass.Reportf(call.Pos(), "[]byte(%s) inside loop — hoist conversion outside to reduce allocations", id.Name)
				}
				return true
			})
			return true
		})
	}
	return nil, nil
}
