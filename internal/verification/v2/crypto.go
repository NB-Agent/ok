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

// ─── Weak Crypto Detector ─────────────────────────────────────────────────
// Detects:
//   1. Hardcoded API keys / tokens / passwords in source
//   2. MD5/SHA1 used for security purposes (not checksums)
//   3. math/rand used for crypto (should be crypto/rand)

type cryptoAnalyzer struct{}

func (cryptoAnalyzer) Name() string        { return "weak-crypto" }
func (cryptoAnalyzer) Layer() string       { return "semantic" }
func (cryptoAnalyzer) Languages() []string { return []string{"go"} }

// isKnownCryptoFP returns true for paths that are known false positives:
//   - acp/: WebSocket handshake (RFC 6455 mandates SHA-1)
//   - digest.go / ok-digest/: hash tools for non-crypto digest computation
//   - database.go: SQL driver usage is not crypto
func isKnownCryptoFP(path string) bool {
	lower := strings.ToLower(path)
	if strings.Contains(lower, string(filepath.Separator)+"acp"+string(filepath.Separator)) {
		return true
	}
	if strings.HasSuffix(lower, "digest.go") || strings.Contains(lower, "ok-digest") {
		return true
	}
	if strings.HasSuffix(lower, "database.go") {
		return true
	}
	return false
}

func (cryptoAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	var ff []Finding
	//nolint:errcheck // callback handles err internally
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.Contains(path, "/vendor/") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}

		// Check hardcoded secrets.
		ast.Inspect(f, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok || len(vs.Values) == 0 {
				return true
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					break
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val := strings.ToLower(lit.Value)
				if isSecretVar(name.Name) && len(val) > 10 && !strings.Contains(val, "os.Getenv") && !strings.Contains(val, "env") {
					ff = append(ff, Finding{
						Analyzer: "weak-crypto",
						Layer:    "semantic",
						Severity: SevHigh,
						File:     path,
						Line:     fset.Position(vs.Pos()).Line,
						Column:   fset.Position(vs.Pos()).Column,
						Message:  "hardcoded secret in variable " + name.Name + " — move to environment variable or vault",
						Category: "security",
						Rule:     "CRYPTO-001",
					})
				}
			}
			return true
		})

		// Skip known false-positive files before MD5/SHA1 check.
		if isKnownCryptoFP(path) {
			return nil
		}

		// Check MD5/SHA1 usage in non-test files.
		if !strings.HasSuffix(path, "_test.go") {
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok2 := sel.X.(*ast.Ident)
				if !ok2 {
					return true
				}
				switch {
				case (pkg.Name == "md5" || pkg.Name == "sha1") && (sel.Sel.Name == "New" || sel.Sel.Name == "Sum"):
					ff = append(ff, Finding{
						Analyzer: "weak-crypto",
						Layer:    "semantic",
						Severity: SevHigh,
						File:     path,
						Line:     fset.Position(n.Pos()).Line,
						Column:   fset.Position(n.Pos()).Column,
						Message:  pkg.Name + "." + sel.Sel.Name + " is cryptographically broken — use sha256",
						Category: "security",
						Rule:     "CRYPTO-002",
					})
				case pkg.Name == "rand" && sel.Sel.Name == "Intn" && !strings.Contains(path, "_test.go"):
					if !importsPackage(f, "crypto/rand") {
						ff = append(ff, Finding{
							Analyzer: "weak-crypto",
							Layer:    "semantic",
							Severity: SevMedium,
							File:     path,
							Line:     fset.Position(n.Pos()).Line,
							Column:   fset.Position(n.Pos()).Column,
							Message:  "math/rand used for random values — use crypto/rand for security-sensitive randomness",
							Category: "security",
							Rule:     "CRYPTO-003",
						})
					}
				}
				return true
			})
		}
		return nil
	})
	return ff, nil
}

func isSecretVar(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range []string{"password", "secret", "token", "apikey", "api_key", "passwd", "pwd", "key", "credential", "private_key"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func importsPackage(f *ast.File, pkg string) bool {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == pkg {
			return true
		}
	}
	return false
}
