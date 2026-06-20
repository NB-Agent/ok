//go:build treesitter

package semantic

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/log"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// skipDirsTS lists directories excluded from chunking.
var skipDirsTS = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"target": true, "build": true, "dist": true,
	".ok": true, ".codegraph": true,
}

type tsQueryDef struct {
	kind  string
	query string
}

// --- Go queries ---
var goTreeQueries = []tsQueryDef{
	{"function", `(function_declaration name: (identifier) @name) @def`},
	{"method", `(method_declaration name: (field_identifier) @name) @def`},
	{"struct", `(type_declaration (type_spec name: (type_identifier) @name type: (struct_type)) @def) @def`},
	{"interface", `(type_declaration (type_spec name: (type_identifier) @name type: (interface_type)) @def) @def`},
}

// --- TypeScript queries ---
var tsTreeQueries = []tsQueryDef{
	{"function", `(function_declaration name: (identifier) @name) @def`},
	{"method", `(method_definition name: (property_identifier) @name) @def`},
	{"class", `(class_declaration name: (type_identifier) @name) @def`},
	{"interface", `(interface_declaration name: (type_identifier) @name) @def`},
}

// --- Python queries ---
var pyTreeQueries = []tsQueryDef{
	{"function", `(function_definition name: (identifier) @name) @def`},
	{"class", `(class_definition name: (identifier) @name) @def`},
}

// ChunkDir walks root and extracts code chunks using tree-sitter for accurate AST parsing.
func (c *Chunker) ChunkDir(root string) ([]Chunk, error) {
	// Initialize parsers
	goParser := sitter.NewParser()
	goParser.SetLanguage(golang.GetLanguage())

	tsParser := sitter.NewParser()
	tsParser.SetLanguage(typescript.GetLanguage())

	pyParser := sitter.NewParser()
	pyParser.SetLanguage(python.GetLanguage())

	var chunks []Chunk

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skipDirsTS[info.Name()] || strings.HasPrefix(info.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		var parser *sitter.Parser
		var lang string
		var queries []tsQueryDef

		switch ext {
		case ".go":
			parser, lang = goParser, "go"
			queries = goTreeQueries
		case ".ts", ".tsx":
			parser, lang = tsParser, "typescript"
			queries = tsTreeQueries
		case ".py":
			parser, lang = pyParser, "python"
			queries = pyTreeQueries
		default:
			return nil
		}

		chunks = append(chunks, extractChunks(parser, path, root, lang, queries)...)
		return nil
	})

	return chunks, err
}

// extractChunks parses a single file and extracts Chunks from it.
func extractChunks(parser *sitter.Parser, path, root, lang string, queries []tsQueryDef) []Chunk {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	tree, err := parser.ParseCtx(sitter.NewParseContext(), nil, data)
	if err != nil || tree == nil {
		return nil
	}
	defer log.Close("tree-sitter", tree)

	rootNode := tree.RootNode()
	relPath, _ := filepath.Rel(root, path)
	lines := strings.Split(string(data), "\n")

	var chunks []Chunk

	for _, qd := range queries {
		q, err := sitter.NewQuery([]byte(qd.query), parser.Language())
		if err != nil {
			continue
		}
		qc := sitter.NewQueryCursor()
		qc.Exec(q, rootNode)

		for {
			m, ok := qc.NextMatch()
			if !ok {
				break
			}

			var name, content string
			var defNode *sitter.Node

			for _, cap := range m.Captures {
				switch cap.Index {
				case 0: // @name
					name = cap.Node.Content(data)
				default: // @def — the full definition node
					defNode = cap.Node
					// Extract full source text including body
					content = cap.Node.Content(data)
				}
			}

			if name == "" {
				continue
			}
			if defNode == nil {
				continue
			}

			line := int(defNode.StartPoint().Row) + 1
			doc := extractDocTS(lines, int(defNode.StartPoint().Row), data, defNode.StartByte())

			// Truncate content to a reasonable size for embedding (max ~2000 chars)
			if len(content) > 2000 {
				// Keep the first 1500 chars + indication
				content = content[:1500] + "\n// ... (truncated)"
			}

			chunks = append(chunks, Chunk{
				ID:       relPath + ":" + itoaTS(line) + ":" + qd.kind + ":" + name,
				File:     relPath,
				Line:     line,
				Content:  content,
				Language: lang,
				Kind:     qd.kind,
				Doc:      doc,
			})
		}
		if cerr := qc.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "treesitter: query cursor close: %v\n", cerr)
		}
		if cerr := q.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "treesitter: query close: %v\n", cerr)
		}
	}

	return chunks
}

// extractDocTS finds the doc comment immediately preceding a definition node.
// startByte is the byte offset of the definition node; we scan backwards from
// the line before the definition for comment lines.
func extractDocTS(lines []string, startRow uint32, data []byte, startByte uint32) string {
	if startRow == 0 {
		return ""
	}
	var docs []string
	for i := int(startRow) - 1; i >= 0; i-- {
		if i >= len(lines) {
			continue
		}
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "//") {
			docs = append([]string{strings.TrimPrefix(trimmed, "// ")}, docs...)
		} else if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "#!") {
			docs = append([]string{strings.TrimPrefix(trimmed, "# ")}, docs...)
		} else if trimmed == "" {
			continue
		} else {
			break
		}
	}
	return strings.Join(docs, " ")
}

func itoaTS(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
