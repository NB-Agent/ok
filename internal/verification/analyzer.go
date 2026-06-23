// Package verification provides analyzers for the ok-verify vet tool.
package verification

import (
	"go/ast"
)

func extractIdent(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return extractIdent(v.X) + "." + v.Sel.Name
	case *ast.StarExpr:
		return "*" + extractIdent(v.X)
	default:
		return "?"
	}
}

func hasDeferRecover(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	for _, stmt := range body.List {
		ds, ok := stmt.(*ast.DeferStmt)
		if !ok {
			continue
		}
		if id, ok := ds.Call.Fun.(*ast.Ident); ok && id.Name == "recover" {
			return true
		}
		if fl, ok := ds.Call.Fun.(*ast.FuncLit); ok && fl.Body != nil {
			if hasRecoverCall(fl.Body) {
				return true
			}
		}
	}
	return false
}

func hasRecoverCall(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == "recover" {
			found = true
			return false
		}
		return true
	})
	return found
}
