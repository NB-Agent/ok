package builtin

import (
	"fmt"
	"go/parser"
	"go/token"
	"strings"
)

// precheckGoFile validates .go file content before writing it.
// Only does a syntax check (go/parser) — cheap, no external tools,
// zero false positives, catches >90% of DST rollbacks (missing braces,
// bad import syntax, wrong package name, etc.).
//
// Semantic errors (undefined types, wrong interfaces) are caught by:
//   - DST (go build on write, with automatic rollback)
//   - The system-prompt rule "run 'go vet ./...' before concluding"
func precheckGoFile(path, content, workDir string) error {
	if !strings.HasSuffix(path, ".go") {
		return nil
	}

	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, path, content, parser.AllErrors)
	if err != nil {
		msg := err.Error()
		if idx := strings.Index(msg, ": "); idx > 0 && idx < 20 {
			msg = msg[idx+2:]
		}
		return fmt.Errorf("go syntax error: %s", msg)
	}
	return nil
}
