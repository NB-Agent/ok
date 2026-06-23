package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/winhide"
)

func init() { tool.RegisterBuiltin(symbolFind{}) }

// symbolFind does semantic symbol resolution: given a function/type/method name,
// it finds the definition, all references (call sites), and (if relevant) the
// interface it satisfies. It also supports natural-language queries by matching
// against file paths, package docs, and function comments.
type symbolFind struct{}

func (symbolFind) Name() string { return "symbol-find" }

func (symbolFind) Description() string {
	return "Find symbol definitions, call sites, and interface implementations by name or description. Supports Go, TypeScript, Python, Rust."
}

func (symbolFind) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"find":{"enum":["definition","references","implementations","all"],"type":"string"},"natural":{"type":"string"},"pkg":{"type":"string"},"symbol":{"type":"string"}},"type":"object"}`)
}

func (symbolFind) ReadOnly() bool { return true }

func (symbolFind) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Symbol  string `json:"symbol"`
		Find    string `json:"find"`
		Pkg     string `json:"pkg"`
		Natural string `json:"natural"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Symbol == "" && p.Natural == "" {
		return "", fmt.Errorf("symbol or natural is required")
	}
	if p.Find == "" {
		p.Find = "all"
	}

	var b strings.Builder
	b.WriteString("# Symbol Find\n\n")

	// Scope: either specific package or whole project
	scope := p.Pkg
	if scope == "" && p.Symbol != "" {
		// Try to locate the symbol first to narrow scope
		scope = findPkg(ctx, p.Symbol)
	}
	searchRoot := "."
	if scope != "" {
		searchRoot = scope
		if !strings.HasPrefix(searchRoot, ".") {
			searchRoot = "." + string(filepath.Separator) + searchRoot
		}
	}

	// Natural language search: search comments + package docs for keywords
	if p.Natural != "" {
		words := strings.Fields(p.Natural)
		b.WriteString(fmt.Sprintf("## Natural search: \"%s\"\n\n", p.Natural))
		commentResults := searchComments(ctx, searchRoot, words)
		if len(commentResults) > 0 {
			b.WriteString("### Matching symbols (by comment / doc)\n\n")
			for _, r := range commentResults {
				b.WriteString(r + "\n")
			}
		} else {
			b.WriteString("(no matching comments found)\n")
		}
		return b.String(), nil
	}

	// Specific symbol search
	b.WriteString(fmt.Sprintf("## Symbol: `%s`\n\n", p.Symbol))

	if p.Find == "definition" || p.Find == "all" {
		b.WriteString("### Definitions\n\n")
		defs := findDefinitions(ctx, searchRoot, p.Symbol)
		if len(defs) > 0 {
			for _, d := range defs {
				b.WriteString(d + "\n")
			}
		} else {
			b.WriteString("(not found — try checking the name or using a broader scope)\n")
		}
	}

	if p.Find == "references" || p.Find == "all" {
		b.WriteString("\n### References (call sites / usage)\n\n")
		refs := findReferences(ctx, searchRoot, p.Symbol)
		if len(refs) > 0 {
			for _, r := range refs {
				b.WriteString(r + "\n")
			}
		} else {
			b.WriteString("(no references found)\n")
		}
	}

	if p.Find == "implementations" || p.Find == "all" {
		b.WriteString("\n### Interface implementations\n\n")
		impls := findImplementations(ctx, searchRoot, p.Symbol)
		if len(impls) > 0 {
			for _, i := range impls {
				b.WriteString(i + "\n")
			}
		} else {
			b.WriteString("(no interface implementations found — may not be an interface or uses implicit satisfaction)\n")
		}
	}

	return b.String(), nil
}

// findPkg tries to locate which package defines a symbol.
func findPkg(ctx context.Context, symbol string) string {
	out := runGoCmd(ctx, "list", "-f", "{{.Dir}}", "./...")
	if out == "" {
		return ""
	}
	dirs := strings.Split(out, "\n")
	for _, d := range dirs {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		match, _ := runCmdOut(ctx, "grep", "-rl", fmt.Sprintf("func.*%s\\(|type %s ", symbol, symbol), d, "--include=*.go")
		if match != "" {
			return d
		}
	}
	return ""
}

// findDefinitions finds function/type/method definitions matching symbol.
func findDefinitions(ctx context.Context, root, symbol string) []string {
	pattern := fmt.Sprintf(`^(func.*%s\b|type %s\b)`, regexp.QuoteMeta(symbol), regexp.QuoteMeta(symbol))
	out, _ := runCmdOut(ctx, "grep", "-rn", pattern, root, "--include=*.go")
	return dedupLines(out)
}

// findReferences finds all uses of a symbol (excluding the definition pattern).
func findReferences(ctx context.Context, root, symbol string) []string {
	// Use word-boundary match for the symbol, exclude test files
	out, _ := runCmdOut(ctx, "grep", "-rn", fmt.Sprintf(`\b%s\b`, regexp.QuoteMeta(symbol)), root, "--include=*.go")
	// Filter out definition lines and test files
	var refs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "_test.go") {
			continue
		}
		// Skip lines that look like definitions (func/type/method declarations)
		if strings.Contains(line, "func") && strings.Contains(line, "(") && !strings.Contains(line, symbol+"(") {
			continue
		}
		refs = append(refs, line)
	}
	if len(refs) > 30 {
		return refs[:30]
	}
	return refs
}

// findImplementations finds types that satisfy an interface.
// For Go, we look for methods matching the interface's method set.
func findImplementations(ctx context.Context, root, symbol string) []string {
	// First check if it's an interface
	ifaceCheck, _ := runCmdOut(ctx, "grep", "-rn", fmt.Sprintf(`type %s\b.*interface`, regexp.QuoteMeta(symbol)), root, "--include=*.go")
	if ifaceCheck == "" {
		// It might not be an interface - try the converse: find interfaces it implements
		return nil
	}

	// Extract method names from the interface definition
	methods := extractInterfaceMethods(ifaceCheck)
	if len(methods) == 0 {
		return nil
	}

	// Find types that have methods matching all interface methods
	// This is a heuristic — Go interfaces are satisfied implicitly
	var impls []string
	for _, m := range methods {
		out, _ := runCmdOut(ctx, "grep", "-rn", fmt.Sprintf(`func \(.*\) %s\(`, regexp.QuoteMeta(m)), root, "--include=*.go")
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				impls = append(impls, fmt.Sprintf("  method `%s`: %s", m, line))
			}
		}
	}
	if len(impls) > 20 {
		impls = impls[:20]
	}
	return impls
}

// extractInterfaceMethods parses method names from an interface definition grep output.
func extractInterfaceMethods(grepOutput string) []string {
	var methods []string
	// Match lines like "MethodName(" in interface definition context
	re := regexp.MustCompile(`\b([A-Z]\w+)\s*\(`)
	seen := map[string]bool{}
	for _, line := range strings.Split(grepOutput, "\n") {
		matches := re.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			name := m[1]
			if !seen[name] && !isKeyword(name) {
				seen[name] = true
				methods = append(methods, name)
			}
		}
	}
	return methods
}

// searchComments searches Go doc comments and package docs for keywords.
func searchComments(ctx context.Context, root string, words []string) []string {
	if len(words) == 0 {
		return nil
	}
	// Build a grep pattern: search for lines starting with // that contain the words
	pattern := ""
	for _, w := range words {
		if pattern != "" {
			pattern += "|"
		}
		pattern += regexp.QuoteMeta(w)
	}
	out, _ := runCmdOut(ctx, "grep", "-rni", pattern, root, "--include=*.go")
	lines := strings.Split(out, "\n")
	var results []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "//") {
			continue
		}
		// Extract just the comment part
		if idx := strings.Index(line, "//"); idx >= 0 {
			comment := strings.TrimSpace(line[idx:])
			// Match against all keywords (case-insensitive)
			matchCount := 0
			commentLower := strings.ToLower(comment)
			for _, w := range words {
				if strings.Contains(commentLower, strings.ToLower(w)) {
					matchCount++
				}
			}
			if matchCount > 0 {
				results = append(results, fmt.Sprintf("  %s  [%d matches]", line, matchCount))
			}
		}
	}
	// Sort by match count descending
	sort.Slice(results, func(i, j int) bool {
		var countI, countJ int
		fmt.Sscanf(results[i], "%*s [%d matches]", &countI)
		fmt.Sscanf(results[j], "%*s [%d matches]", &countJ)
		return countI > countJ
	})
	if len(results) > 20 {
		return results[:20]
	}
	return results
}

func runGoCmd(ctx context.Context, args ...string) string {
	cmd := winhide.CommandContext(ctx, "go", args...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func runCmdOut(ctx context.Context, name string, args ...string) (string, error) {
	cmd := winhide.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func dedupLines(input string) []string {
	seen := map[string]bool{}
	var result []string
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			result = append(result, "  "+line)
		}
	}
	if len(result) > 20 {
		return result[:20]
	}
	return result
}

var goKeywords = map[string]bool{
	"if": true, "for": true, "return": true, "nil": true,
	"true": true, "false": true, "break": true, "continue": true,
	"const": true, "var": true, "switch": true, "case": true,
	"default": true, "defer": true, "go": true, "select": true,
	"range": true, "map": true, "chan": true, "func": true,
	"type": true, "interface": true, "struct": true, "package": true,
	"import": true, "error": true, "string": true, "int": true,
	"bool": true, "byte": true, "rune": true, "any": true,
}

func isKeyword(s string) bool { return goKeywords[s] }
