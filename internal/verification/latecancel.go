package verification

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

var LateCancel = &analysis.Analyzer{
	Name: "latecancel",
	Doc:  "checks that defer cancel() is placed close to context.WithCancel to prevent leaks on early returns",
	Run:  runLateCancel,
}

func runLateCancel(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			// Look for ctx, cancel := context.WithCancel/WithTimeout
			if len(as.Lhs) != 2 || len(as.Rhs) != 1 {
				return true
			}
			call, ok := as.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != "context" {
				return true
			}
			if sel.Sel.Name != "WithCancel" && sel.Sel.Name != "WithTimeout" {
				return true
			}

			// Found context.WithCancel/WithTimeout
			// Find the cancel var
			var cancelIdent *ast.Ident
			for _, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && !isCommonCtxName(id.Name) {
					if id.Name == "cancel" || stringsEndsWith(id.Name, "Cancel") {
						cancelIdent = id
						break
					}
				}
			}
			if cancelIdent == nil {
				// Try the second identifier in the LHS
				if len(as.Lhs) == 2 {
					cancelIdent, _ = as.Lhs[1].(*ast.Ident)
				}
			}
			if cancelIdent == nil {
				return true
			}

			// Find defer cancel() - count statements between WithCancel and defer
			funcBody := findEnclosingFuncBody2(file, as)
			if funcBody == nil {
				return true
			}

			deferPos := -1
			startPos := -1
			for i, stmt := range funcBody.List {
				if stmt.Pos() == as.Pos() {
					startPos = i
				}
				if ds, ok := stmt.(*ast.DeferStmt); ok {
					if id, ok := ds.Call.Fun.(*ast.Ident); ok && id.Name == cancelIdent.Name {
						deferPos = i
						break
					}
					// Check for cancel() in defer func
					if fl, ok := ds.Call.Fun.(*ast.FuncLit); ok {
						ast.Inspect(fl.Body, func(n2 ast.Node) bool {
							if id, ok := n2.(*ast.Ident); ok && id.Name == cancelIdent.Name {
								deferPos = i
								return false
							}
							return true
						})
					}
				}
				if deferPos >= 0 {
					break
				}
			}
			if startPos >= 0 && deferPos > startPos+1 {
				pass.Reportf(as.Pos(), "defer %s() not immediately after context.WithCancel — early returns before the defer may leak context", cancelIdent.Name)
			}
			return true
		})
	}
	return nil, nil
}

func findEnclosingFuncBody2(file *ast.File, n ast.Node) *ast.BlockStmt {
	var body *ast.BlockStmt
	ast.Inspect(file, func(parent ast.Node) bool {
		switch v := parent.(type) {
		case *ast.FuncDecl:
			if v.Body != nil && n.Pos() >= v.Pos() && n.End() <= v.End() {
				body = v.Body
				return false
			}
		case *ast.FuncLit:
			if v.Body != nil && n.Pos() >= v.Pos() && n.End() <= v.End() {
				body = v.Body
				return false
			}
		}
		return true
	})
	return body
}

func stringsEndsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// isCommonCtxName returns true for common context variable names that are
// unlikely to be cancel functions, filtering them out from cancel-var detection.
func isCommonCtxName(name string) bool {
	switch name {
	case "ctx", "cctx", "tctx", "dctx", "baseCtx", "parentCtx", "childCtx":
		return true
	}
	return false
}
