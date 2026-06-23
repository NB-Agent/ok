package verification

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var MutexCopy = &analysis.Analyzer{
	Name: "mutexcopy",
	Doc:  "checks that sync.Mutex and sync.RWMutex are not copied by value",
	Run:  runMutexCopy,
}

func runMutexCopy(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.FuncDecl:
				// Check function parameters
				if v.Type.Params != nil {
					for _, param := range v.Type.Params.List {
						if isMutexType(pass, param.Type) {
							pass.Reportf(param.Pos(), "%s is a sync.Mutex passed by value — will copy the lock", paramNames(param))
						}
					}
				}
				// Check function results
				if v.Type.Results != nil {
					for _, result := range v.Type.Results.List {
						if isMutexType(pass, result.Type) {
							pass.Reportf(result.Pos(), "returning sync.Mutex by value — callers receive a copy")
						}
					}
				}
			case *ast.AssignStmt:
				// Check short var declarations: x := y where y is a struct with Mutex
				// (complex, skip for now)
			}
			return true
		})
	}
	return nil, nil
}

func isMutexType(pass *analysis.Pass, expr ast.Expr) bool {
	t := pass.TypesInfo.TypeOf(expr)
	if t == nil {
		return false
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == "sync" &&
		(obj.Name() == "Mutex" || obj.Name() == "RWMutex")
}

func paramNames(field *ast.Field) string {
	if len(field.Names) == 0 {
		return extractIdent(field.Type)
	}
	names := ""
	for i, n := range field.Names {
		if i > 0 {
			names += ", "
		}
		names += n.Name
	}
	return names
}
