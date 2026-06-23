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

// ─── SQL Injection Detector ──────────────────────────────────────────────
// Detects string concatenation in SQL query strings — the most common SQLi
// vector. Matches patterns like:
//   "SELECT * FROM users WHERE id = " + input
//   fmt.Sprintf("SELECT * FROM %s", tableName)
//   db.Query("SELECT * FROM users WHERE name = '" + name + "'")

type sqliAnalyzer struct{}

func (sqliAnalyzer) Name() string        { return "sql-injection" }
func (sqliAnalyzer) Layer() string       { return "semantic" }
func (sqliAnalyzer) Languages() []string { return []string{"go"} }

// isPromptOrSkillFile returns true for files that are prompt templates or
// skill definitions — they contain SQL keywords in prose (security review
// checklists, test descriptions, etc.) that are not actual SQL queries.
func isPromptOrSkillFile(path string) bool {
	lower := strings.ToLower(path)
	if strings.Contains(lower, "skill") && strings.HasSuffix(lower, "builtins.go") {
		return true
	}
	// Prompt template files.
	if strings.HasSuffix(lower, "prompt.go") || strings.HasSuffix(lower, "prompts.go") {
		return true
	}
	return false
}

// skipSQLConcatenation returns true if the concatenation expression is not
// an actual SQL query but rather an AI prompt or skill template that mentions
// SQL as a concept (e.g. "SQL injection" in a security review checklist).
func skipSQLConcatenation(be *ast.BinaryExpr) bool {
	// Count string literal operands vs identifier operands.
	// A real SQL injection pattern typically has string + string or string + ident
	// where the string directly constructs a query. A prompt template has many
	// identifiers mixed with prose-like strings.
	strCount := 0
	idCount := 0
	ast.Inspect(be, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.BasicLit:
			strCount++
		case *ast.Ident:
			idCount++
		}
		return true
	})
	// Heuristic: if the expression has more identifiers than string literals,
	// it's likely a prompt template concatenation, not a SQL query.
	return idCount > strCount*2
}

func (sqliAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	var ff []Finding
	//nolint:errcheck // callback handles err internally
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/vendor/") {
			return nil
		}

		// Skip known false-positive files (skill prompts, templates).
		if isPromptOrSkillFile(path) {
			return nil
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			be, ok := n.(*ast.BinaryExpr)
			if !ok || be.Op != token.ADD {
				return true
			}
			// Skip files that are prompt templates with many identifiers.
			if skipSQLConcatenation(be) {
				return true
			}
			// Check if either operand contains SQL keywords.
			if hasSQLKeyword(be) {
				ff = append(ff, Finding{
					Analyzer: "sql-injection",
					Layer:    "semantic",
					Severity: SevHigh,
					File:     path,
					Line:     fset.Position(be.Pos()).Line,
					Column:   fset.Position(be.Pos()).Column,
					Message:  "SQL string built with concatenation — use parameterized queries",
					Category: "security",
					Rule:     "SQLI-001",
				})
			}
			return true
		})
		return nil
	})
	return ff, nil
}

func hasSQLKeyword(be *ast.BinaryExpr) bool {
	// Walk the expression tree looking for SQL keywords in string literals.
	found := false
	ast.Inspect(be, func(n ast.Node) bool {
		if found {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		val := strings.ToUpper(strings.Trim(lit.Value, `"`+"`"))
		for _, kw := range sqlKeywords {
			if strings.Contains(val, kw) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

var sqlKeywords = []string{
	"SELECT ", "INSERT ", "UPDATE ", "DELETE ", "DROP ", "CREATE ",
	"ALTER ", "GRANT ", "REVOKE ", "TRUNCATE ", "MERGE ",
}
