//go:build treesitter

package agent

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// tsParser holds a tree-sitter parser for a named language.
type tsParser struct {
	lang string
	p    *sitter.Parser
}

var tsParsers []tsParser

// tsQueries maps language → tree-sitter query patterns that capture
// structural declarations (function, method, type, import, etc.).
// Each query captures the first line of the matched node.
var tsQueries = map[string][]string{
	"go": {
		"(function_declaration name: (identifier) @name)",
		"(method_declaration name: (field_identifier) @name)",
		"(type_declaration (type_spec name: (type_identifier) @name))",
		"(import_spec path: (interpreted_string_literal) @path)",
	},
	"python": {
		"(function_definition name: (identifier) @name)",
		"(class_definition name: (identifier) @name)",
		"(import_statement name: (dotted_name) @name)",
		"(import_from_statement module_name: (dotted_name) @name)",
	},
	"typescript": {
		"(function_declaration name: (identifier) @name)",
		"(method_definition name: (property_identifier) @name)",
		"(class_declaration name: (type_identifier) @name)",
		"(interface_declaration name: (type_identifier) @name)",
		"(enum_declaration name: (identifier) @name)",
		"(import_statement source: (string) @path)",
		"(export_statement (function_declaration name: (identifier) @name))",
	},
}

func init() {
	add := func(lang string, getGrammar func() *sitter.Language) {
		p := sitter.NewParser()
		p.SetLanguage(getGrammar())
		tsParsers = append(tsParsers, tsParser{lang: lang, p: p})
	}
	add("go", golang.GetLanguage)
	add("python", python.GetLanguage)
	add("typescript", typescript.GetLanguage)
}

// detectLanguageFromContent infers the programming language from file content
// by examining the first few lines for language-specific patterns.
func detectLanguageFromContent(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	// Examine first 10 lines (or fewer for short files).
	maxCheck := 10
	if len(lines) < maxCheck {
		maxCheck = len(lines)
	}

	// Check each line for language-specific patterns.
	// Score-based detection: each pattern adds to a language's confidence.
	scores := make(map[string]int)
	for i := 0; i < maxCheck; i++ {
		line := strings.TrimSpace(lines[i])

		// Shebang lines — definitive signal.
		if strings.HasPrefix(line, "#!/usr/bin/env python") || strings.HasPrefix(line, "#!/usr/bin/python") {
			scores["python"] += 10
			continue
		}
		if strings.HasPrefix(line, "#!/usr/bin/env node") || strings.HasPrefix(line, "#!/bin/sh") || strings.HasPrefix(line, "#!/bin/bash") {
			scores["bash"] += 10
			continue
		}

		switch {
		case strings.HasPrefix(line, "package ") && !strings.HasPrefix(line, "package.json"):
			scores["go"] += 5
		case strings.HasPrefix(line, "import ("):
			scores["go"] += 3
		case strings.HasPrefix(line, "func ") || strings.HasPrefix(line, "func ("):
			scores["go"] += 4
		case strings.HasPrefix(line, "type ") && (strings.Contains(line, " struct") || strings.Contains(line, " interface")):
			scores["go"] += 3
		case strings.HasPrefix(line, "def ") || strings.HasPrefix(line, "async def "):
			scores["python"] += 4
		case strings.HasPrefix(line, "class ") && strings.HasSuffix(line, ":"):
			scores["python"] += 3
		case strings.HasPrefix(line, "import ") && strings.Contains(line, "from") == false:
			scores["python"] += 2
		case strings.HasPrefix(line, "from ") && strings.Contains(line, "import"):
			scores["python"] += 2
		case strings.HasPrefix(line, "fn ") || strings.HasPrefix(line, "pub fn "):
			scores["rust"] += 4
		case strings.HasPrefix(line, "function ") || strings.HasPrefix(line, "async function "):
			scores["typescript"] += 4
		case strings.HasPrefix(line, "interface ") && strings.Contains(line, "{"):
			scores["typescript"] += 3
		case strings.HasPrefix(line, "export ") && (strings.Contains(line, "function") || strings.Contains(line, "class") || strings.Contains(line, "interface")):
			scores["typescript"] += 3
		case strings.HasPrefix(line, "const ") || strings.HasPrefix(line, "let ") || strings.HasPrefix(line, "var "):
			scores["typescript"] += 1
		case strings.HasPrefix(line, "use "):
			scores["rust"] += 2
		case strings.HasPrefix(line, "struct ") || strings.HasPrefix(line, "enum "):
			// Ambiguous between Go, Rust, C/C++ — prefer Rust if Go not already strong.
			if scores["go"] < 3 {
				scores["rust"] += 2
			} else {
				scores["go"] += 2
			}
		}
	}

	// Find the highest-scoring language; require at least 3 points to avoid
	// false positives on short or ambiguous snippets.
	best := ""
	bestScore := 0
	for lang, score := range scores {
		if score > bestScore {
			bestScore = score
			best = lang
		}
	}
	if bestScore >= 3 {
		return best
	}
	// For Go files, even a single "package " line is very reliable.
	if scores["go"] >= 5 {
		return "go"
	}
	return ""
}

// extractStructuralLinesTS uses tree-sitter to extract structural declaration
// lines from a file's content. Returns the exact lines (0-based) of function
// signatures, method declarations, type definitions, and import statements.
// Returns nil when language can't be detected, no grammar is available, or
// parsing fails — the caller should fall back to heuristic extraction.
func extractStructuralLinesTS(lines []string) []string {
	if len(lines) < 3 {
		return nil
	}

	lang := detectLanguageFromContent(lines)
	if lang == "" {
		return nil
	}

	// Find matching parser.
	var parser *sitter.Parser
	for _, tp := range tsParsers {
		if tp.lang == lang {
			parser = tp.p
			break
		}
	}
	if parser == nil {
		return nil
	}

	// Reconstruct file content from lines.
	content := []byte(strings.Join(lines, "\n"))

	tree, err := parser.ParseCtx(sitter.NewParseContext(), nil, content)
	if err != nil || tree == nil {
		return nil
	}
	defer tree.Close()

	root := tree.RootNode()
	queries, ok := tsQueries[lang]
	if !ok {
		return nil
	}

	// Collect structural line numbers.
	lineSet := make(map[uint32]bool)
	for _, qs := range queries {
		q, err := sitter.NewQuery([]byte(qs), parser.Language())
		if err != nil {
			continue
		}
		qc := sitter.NewQueryCursor()
		qc.Exec(q, root)

		for {
			m, ok := qc.NextMatch()
			if !ok {
				break
			}
			for _, cap := range m.Captures {
				// Use the start line of the captured node.
				lineSet[cap.Node.StartPoint().Row] = true
			}
		}
		qc.Close()
		q.Close()
	}

	if len(lineSet) == 0 {
		return nil
	}

	// Convert set of line numbers to sorted string lines.
	// Collect line numbers first, then sort.
	maxLine := uint32(len(lines) - 1)
	var result []string
	for row := uint32(0); row <= maxLine; row++ {
		if lineSet[row] {
			result = append(result, lines[row])
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}
