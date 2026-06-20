package v2

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// ─── Resource Leak Detector ───────────────────────────────────────────────
// Detects os.File / net.Conn opened without corresponding defer-Close,
// and goroutines spawned without defer-recover.

type leakAnalyzer struct{}

func (leakAnalyzer) Name() string        { return "resource-leak" }
func (leakAnalyzer) Layer() string       { return "semantic" }
func (leakAnalyzer) Languages() []string { return []string{"go"} }

func (leakAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	var ff []Finding
	//nolint:errcheck // callback handles err internally
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/vendor/") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}

		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			opens := findResourceOpens(fd.Body)
			closes := findResourceCloses(fd.Body)

			for _, open := range opens {
				found := false
				for _, cls := range closes {
					if cls == open.name {
						found = true
						break
					}
				}
				if !found {
					ff = append(ff, Finding{
						Analyzer: "resource-leak",
						Layer:    "semantic",
						Severity: SevMedium,
						File:     path,
						Line:     fset.Position(open.pos).Line,
						Column:   fset.Position(open.pos).Column,
						Message:  "resource " + open.name + " opened without defer-close in " + fd.Name.Name + " — may leak",
						Category: "correctness",
						Rule:     "LEAK-001",
					})
				}
			}

			// Check goroutines for defer-recover.
			goroutines := findGoroutines(fd.Body)
			for _, gs := range goroutines {
				body := getGoBody(gs)
				if body != nil && !hasDeferRecoverInBlock(body) {
					ff = append(ff, Finding{
						Analyzer: "resource-leak",
						Layer:    "semantic",
						Severity: SevMedium,
						File:     path,
						Line:     fset.Position(gs.Pos()).Line,
						Column:   fset.Position(gs.Pos()).Column,
						Message:  "goroutine in " + fd.Name.Name + " missing defer-recover — panic will crash process",
						Category: "correctness",
						Rule:     "LEAK-002",
					})
				}
			}
		}
		return nil
	})
	return ff, nil
}

type resRef struct {
	name string
	pos  token.Pos
}

func findResourceOpens(body *ast.BlockStmt) []resRef {
	var refs []resRef
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Rhs) == 0 {
			return true
		}
		for _, rhs := range as.Rhs {
			ce, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := ce.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			if isResourceOpen(sel.Sel.Name) && len(as.Lhs) > 0 {
				if id, ok := as.Lhs[0].(*ast.Ident); ok {
					refs = append(refs, resRef{id.Name, ce.Pos()})
				}
			}
		}
		return true
	})
	return refs
}

func isResourceOpen(name string) bool {
	switch name {
	case "Open", "OpenFile", "Create", "Dial", "DialContext", "Listen":
		return true
	}
	return false
}

func findResourceCloses(body *ast.BlockStmt) []string {
	var names []string
	ast.Inspect(body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Close" {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			names = append(names, id.Name)
		}
		return true
	})
	return names
}

func findGoroutines(body *ast.BlockStmt) []*ast.GoStmt {
	var gs []*ast.GoStmt
	ast.Inspect(body, func(n ast.Node) bool {
		if g, ok := n.(*ast.GoStmt); ok {
			gs = append(gs, g)
		}
		return true
	})
	return gs
}

func getGoBody(gs *ast.GoStmt) *ast.BlockStmt {
	if fl, ok := gs.Call.Fun.(*ast.FuncLit); ok {
		return fl.Body
	}
	return nil
}

func hasDeferRecoverInBlock(body *ast.BlockStmt) bool {
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
			found := false
			ast.Inspect(fl.Body, func(n ast.Node) bool {
				if found {
					return false
				}
				if id, ok := n.(*ast.Ident); ok && id.Name == "recover" {
					found = true
				}
				return !found
			})
			if found {
				return true
			}
		}
	}
	return false
}
