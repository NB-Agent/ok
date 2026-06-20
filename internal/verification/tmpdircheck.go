package verification

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var TmpDirCheck = &analysis.Analyzer{
	Name: "tmpdircheck",
	Doc:  "checks that os.MkdirTemp is paired with defer os.RemoveAll",
	Run:  runTmpDirCheck,
}

func runTmpDirCheck(pass *analysis.Pass) (interface{}, error) {
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
			xIdent, ok := sel.X.(*ast.Ident)
			if !ok || xIdent.Name != "os" || sel.Sel.Name != "MkdirTemp" {
				return true
			}
			// Found os.MkdirTemp — check for defer os.RemoveAll in the same function
			funcBody := findEnclosingFuncBody(file, call)
			if funcBody == nil {
				return true
			}
			hasCleanup := false
			ast.Inspect(funcBody, func(n2 ast.Node) bool {
				if hasCleanup {
					return false
				}
				ds, ok := n2.(*ast.DeferStmt)
				if !ok {
					return true
				}
				dsCall, ok := ds.Call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := dsCall.X.(*ast.Ident)
				if ok && id.Name == "os" && dsCall.Sel.Name == "RemoveAll" {
					hasCleanup = true
					return false
				}
				return true
			})
			if !hasCleanup {
				pass.Reportf(call.Pos(), "os.MkdirTemp without defer os.RemoveAll — temporary directory may leak")
			}
			return true
		})
	}
	return nil, nil
}

func findEnclosingFuncBody(file *ast.File, n ast.Node) *ast.BlockStmt {
	var body *ast.BlockStmt
	ast.Inspect(file, func(parent ast.Node) bool {
		switch v := parent.(type) {
		case *ast.FuncDecl:
			if n.Pos() >= v.Pos() && n.End() <= v.End() {
				if strings.Contains(v.Name.Name, "Test") {
					return false // skip test files
				}
				body = v.Body
				return false
			}
		case *ast.FuncLit:
			if n.Pos() >= v.Pos() && n.End() <= v.End() {
				body = v.Body
				return false
			}
		}
		return true
	})
	return body
}
