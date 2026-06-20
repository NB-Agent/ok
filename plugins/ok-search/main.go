// @ok/search — MCP plugin: Code search across the codebase.
// Provides regex code search, file search, and symbol lookup
// without any external dependencies (no tree-sitter / Ollama).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func main() {
	s := &mcpServer{name: "ok-search", version: "1.0.0"}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for dec.More() {
		var req jsonRPC
		if err := dec.Decode(&req); err != nil {
			break
		}
		resp := s.handle(req)
		if resp.ID != nil {
			enc.Encode(resp)
		}
	}
}

type jsonRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResp struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      *int        `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *jsonError  `json:"error,omitempty"`
}

type jsonError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpServer struct {
	name    string
	version string
}

func (s *mcpServer) handle(req jsonRPC) jsonRPCResp {
	switch req.Method {
	case "initialize":
		return jsonRPCResp{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]string{"name": s.name, "version": s.version},
				"capabilities":    map[string]interface{}{"tools": map[string]bool{}},
			},
		}
	case "tools/list":
		return jsonRPCResp{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"tools": s.tools()},
		}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &p)
		content, err := s.callTool(p.Name, p.Arguments)
		if err != nil {
			return jsonRPCResp{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "Error: " + err.Error()}}, "isError": true},
			}
		}
		return jsonRPCResp{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": content}}},
		}
	default:
		return jsonRPCResp{JSONRPC: "2.0", ID: req.ID, Error: &jsonError{Code: -32601, Message: "method not found: " + req.Method}}
	}
}

func (s *mcpServer) tools() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "search-code",
			"description": "Search code for a regex pattern. Returns file:line matches with context. Supports language filters (go/py/js/ts/sh/md). Falls back to recursive grep.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern":          map[string]interface{}{"type": "string", "description": "Regex pattern to search for (RE2 syntax)"},
					"path":             map[string]interface{}{"type": "string", "description": "Directory to search (default: current project root)"},
					"lang":             map[string]interface{}{"type": "string", "description": "Language filter: go/py/js/ts/sh/md/html/css. Omit for all files."},
					"max":              map[string]interface{}{"type": "integer", "description": "Max results (default 50, max 200)"},
					"case_insensitive": map[string]interface{}{"type": "boolean", "description": "Case-insensitive search (default false)"},
				},
				"required": []string{"pattern"},
			},
		},
		{
			"name":        "search-files",
			"description": "Find files by name pattern (glob). Supports * ? []. Returns relative paths sorted by name.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{"type": "string", "description": "Glob pattern, e.g. '*.go', '**/test_*.go', 'internal/**/*.go'"},
					"path":    map[string]interface{}{"type": "string", "description": "Directory to search (default: current project root)"},
					"max":     map[string]interface{}{"type": "integer", "description": "Max results (default 100)"},
				},
				"required": []string{"pattern"},
			},
		},
		{
			"name":        "search-symbols",
			"description": "Find Go symbol definitions and references. Searches func/type/interface/method declarations and their call sites using grep patterns — no tree-sitter dependency.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbol": map[string]interface{}{"type": "string", "description": "Symbol name to find (func name, type name, interface name)"},
					"find":   map[string]interface{}{"type": "string", "enum": []string{"definition", "references", "implementations", "all"}, "description": "What to find (default: all)"},
					"pkg":    map[string]interface{}{"type": "string", "description": "Limit to a Go package path like 'internal/control'"},
				},
				"required": []string{"symbol"},
			},
		},
	}
}

func (s *mcpServer) callTool(name string, args json.RawMessage) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch name {
	case "search-code":
		return searchCode(ctx, args)
	case "search-files":
		return searchFiles(ctx, args)
	case "search-symbols":
		return searchSymbols(ctx, args)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// ─── search-code ─────────────────────────────────────────────

func searchCode(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Pattern         string `json:"pattern"`
		Path            string `json:"path"`
		Lang            string `json:"lang"`
		Max             int    `json:"max"`
		CaseInsensitive bool   `json:"case_insensitive"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if p.Path == "" {
		p.Path = "."
	}
	if p.Max <= 0 {
		p.Max = 50
	}
	if p.Max > 200 {
		p.Max = 200
	}

	grepArgs := []string{"-rn", "--color=never"}
	if p.CaseInsensitive {
		grepArgs = append(grepArgs, "-i")
	}
	// Build include args based on language
	include := langInclude(p.Lang)
	grepArgs = append(grepArgs, include...)
	grepArgs = append(grepArgs, "-m", fmt.Sprintf("%d", p.Max+1))
	grepArgs = append(grepArgs, p.Pattern, p.Path)

	out, _ := runCmd(ctx, "grep", grepArgs...)
	lines := strings.Split(out, "\n")
	if len(lines) > p.Max {
		lines = lines[:p.Max]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Code Search: `%s`\n\n", p.Pattern))
	if len(lines) == 1 && lines[0] == "" {
		b.WriteString("No matches found.\n")
	} else {
		b.WriteString(fmt.Sprintf("Found %d match(es):\n\n", countNonEmpty(lines)))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				b.WriteString("  " + line + "\n")
			}
		}
	}
	return b.String(), nil
}

func langInclude(lang string) []string {
	switch lang {
	case "go":
		return []string{"--include=*.go"}
	case "py":
		return []string{"--include=*.py"}
	case "js":
		return []string{"--include=*.js", "--include=*.jsx"}
	case "ts":
		return []string{"--include=*.ts", "--include=*.tsx"}
	case "sh":
		return []string{"--include=*.sh", "--include=*.bash"}
	case "md":
		return []string{"--include=*.md"}
	case "html":
		return []string{"--include=*.html", "--include=*.htm"}
	case "css":
		return []string{"--include=*.css"}
	case "toml":
		return []string{"--include=*.toml"}
	case "json":
		return []string{"--include=*.json"}
	case "yaml", "yml":
		return []string{"--include=*.yaml", "--include=*.yml"}
	default:
		return nil
	}
}

// ─── search-files ────────────────────────────────────────────

func searchFiles(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Max     int    `json:"max"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if p.Path == "" {
		p.Path = "."
	}
	if p.Max <= 0 {
		p.Max = 100
	}
	if p.Max > 500 {
		p.Max = 500
	}

	// Use bash find for recursive glob matching
	// Convert glob like **/*.go to -name '*.go'
	pattern := p.Pattern
	// Strip **/ prefix if present
	cleanPattern := strings.TrimPrefix(pattern, "**/")
	// Handle ** in middle by using -path
	findArgs := []string{"-type", "f", "-name", cleanPattern}
	if strings.Contains(pattern, "**/") {
		// More complex pattern — just use the raw pattern with -path
		findArgs = []string{"-type", "f", "-path", pattern}
	}

	args2 := append([]string{p.Path}, findArgs...)
	args2 = append(args2, "-not", "-path", "*/.git/*", "-not", "-path", "*/node_modules/*")
	out, _ := runCmd(ctx, "find", args2...)

	lines := strings.Split(out, "\n")
	// Filter and sort
	var results []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			results = append(results, line)
		}
	}
	sort.Strings(results)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# File Search: `%s`\n\n", p.Pattern))
	if len(results) == 0 {
		b.WriteString("No files found.\n")
	} else {
		total := len(results)
		if total > p.Max {
			results = results[:p.Max]
		}
		b.WriteString(fmt.Sprintf("Showing %d of %d file(s):\n\n", len(results), total))
		for _, f := range results {
			b.WriteString("  " + f + "\n")
		}
	}
	return b.String(), nil
}

// ─── search-symbols ──────────────────────────────────────────

func searchSymbols(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Symbol string `json:"symbol"`
		Find   string `json:"find"`
		Pkg    string `json:"pkg"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Symbol == "" {
		return "", fmt.Errorf("symbol is required")
	}
	if p.Find == "" {
		p.Find = "all"
	}

	root := "."
	if p.Pkg != "" {
		root = filepath.Join(".", p.Pkg)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Symbol: `%s`\n\n", p.Symbol))

	if p.Find == "definition" || p.Find == "all" {
		b.WriteString("## Definitions\n\n")
		pat := fmt.Sprintf(`^(func\s+(\([^)]+\)\s+)?%s\b|type\s+%s\b)`, regexp.QuoteMeta(p.Symbol), regexp.QuoteMeta(p.Symbol))
		out, _ := runCmd(ctx, "grep", "-rn", pat, root, "--include=*.go")
		writeLines(&b, out)
	}

	if p.Find == "references" || p.Find == "all" {
		b.WriteString("\n## References\n\n")
		pat := fmt.Sprintf(`\b%s\b`, regexp.QuoteMeta(p.Symbol))
		out, _ := runCmd(ctx, "grep", "-rn", pat, root, "--include=*.go")
		// Filter out lines from _test.go and def lines
		var refs []string
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.Contains(line, "_test.go") {
				continue
			}
			refs = append(refs, line)
		}
		if len(refs) > 30 {
			refs = refs[:30]
		}
		for _, r := range refs {
			b.WriteString("  " + r + "\n")
		}
		if len(refs) == 0 {
			b.WriteString("  (no references found)\n")
		}
	}

	if p.Find == "implementations" || p.Find == "all" {
		b.WriteString("\n## Interface Implementations\n\n")
		// Check if it's an interface
		ifacePat := fmt.Sprintf(`type\s+%s\b.*interface`, regexp.QuoteMeta(p.Symbol))
		ifaceOut, _ := runCmd(ctx, "grep", "-rn", ifacePat, root, "--include=*.go")
		if ifaceOut == "" {
			b.WriteString("  (not an interface, or not found)\n")
		} else {
			methods := extractMethods(ifaceOut)
			if len(methods) == 0 {
				b.WriteString("  (no methods found in interface)\n")
			} else {
				b.WriteString(fmt.Sprintf("  Methods: %s\n\n", strings.Join(methods, ", ")))
				for _, m := range methods {
					out, _ := runCmd(ctx, "grep", "-rn", fmt.Sprintf(`func\s+\([^)]+\)\s+%s\(`, regexp.QuoteMeta(m)), root, "--include=*.go")
					writeLines(&b, out)
				}
			}
		}
	}

	return b.String(), nil
}

func extractMethods(grepOut string) []string {
	re := regexp.MustCompile(`\b([A-Z]\w+)\s*\(`)
	seen := map[string]bool{}
	var methods []string
	for _, line := range strings.Split(grepOut, "\n") {
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			name := m[1]
			if !seen[name] && !goKeyword(name) {
				seen[name] = true
				methods = append(methods, name)
			}
		}
	}
	sort.Strings(methods)
	return methods
}

func writeLines(b *strings.Builder, out string) {
	lines := strings.Split(out, "\n")
	var count int
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			b.WriteString("  " + line + "\n")
			count++
		}
	}
	if count == 0 {
		b.WriteString("  (not found)\n")
	}
}

func countNonEmpty(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
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

func goKeyword(s string) bool { return goKeywords[s] }
