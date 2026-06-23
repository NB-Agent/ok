package verification

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var PreallocSlice = &analysis.Analyzer{
	Name: "preallocslice",
	Doc:  "checks that slices with known size are pre-allocated to avoid repeated growth",
	Run:  runPreallocSlice,
}

func runPreallocSlice(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			rangeStmt, ok := n.(*ast.RangeStmt)
			if !ok || rangeStmt.Body == nil {
				return true
			}

			// Find the block that contains the range statement.
			parentBlock := findParentBlock(file, rangeStmt)
			if parentBlock == nil {
				return true
			}

			// Collect slice variables declared before the range statement in the same block.
			// We look for `var xs []T` declarations that are used in append() inside the range body.
			precedingSlices := make(map[string]bool) // var names declared as []T before range
			foundRange := false
			for _, stmt := range parentBlock.List {
				if stmt == rangeStmt {
					foundRange = true
					break
				}
				if ds, ok := stmt.(*ast.DeclStmt); ok {
					if gd, ok := ds.Decl.(*ast.GenDecl); ok {
						for _, spec := range gd.Specs {
							if vs, ok := spec.(*ast.ValueSpec); ok {
								if len(vs.Values) == 0 {
									// var x []T
									if arr, ok := vs.Type.(*ast.ArrayType); ok && arr.Len == nil {
										for _, name := range vs.Names {
											precedingSlices[name.Name] = true
										}
									}
									// Also match var x = make([]T, 0) — already pre-allocated, skip
								}
							}
						}
					}
				}
				// Also check short var decls like xs := []T{}
				if as, ok := stmt.(*ast.AssignStmt); ok {
					for _, rhs := range as.Rhs {
						if cl, ok := rhs.(*ast.CompositeLit); ok {
							if arr, ok := cl.Type.(*ast.ArrayType); ok && arr.Len == nil && len(cl.Elts) == 0 {
								for _, lhs := range as.Lhs {
									if id, ok := lhs.(*ast.Ident); ok {
										precedingSlices[id.Name] = true
									}
								}
							}
						}
					}
				}
			}

			if !foundRange || len(precedingSlices) == 0 {
				return true
			}

			// Now check the range body for append() calls using these slice vars.
			ast.Inspect(rangeStmt.Body, func(n2 ast.Node) bool {
				call, ok := n2.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "append" {
					return true
				}
				if len(call.Args) < 1 {
					return true
				}
				id, ok := call.Args[0].(*ast.Ident)
				if !ok {
					return true
				}
				if !precedingSlices[id.Name] {
					return true
				}

				// Found: var xs []T before range; xs = append(xs, ...) inside range body.
				// Verify xs is actually a slice type via type info.
				obj := pass.TypesInfo.ObjectOf(id)
				if obj == nil {
					return true
				}
				if _, ok := obj.Type().Underlying().(*types.Slice); !ok {
					return true
				}

				pass.Reportf(id.Pos(), "%s is appended inside a range loop but was not pre-allocated — use make([]T, 0, len) to avoid repeated slice growth", id.Name)
				return false
			})
			return true
		})
	}
	return nil, nil
}

func findParentBlock(file *ast.File, n ast.Node) *ast.BlockStmt {
	var blk *ast.BlockStmt
	ast.Inspect(file, func(parent ast.Node) bool {
		switch v := parent.(type) {
		case *ast.BlockStmt:
			for _, stmt := range v.List {
				if stmt == n {
					blk = v
					return false
				}
			}
		}
		return true
	})
	return blk
}
