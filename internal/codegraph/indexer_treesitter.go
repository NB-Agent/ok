//go:build treesitter

package codegraph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/NB-Agent/ok/internal/log"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

type SymbolKind int

const (
	SymbolFunction SymbolKind = iota
	SymbolMethod
	SymbolStruct
	SymbolInterface
	SymbolClass
	SymbolImport
	SymbolVariable
	SymbolType
)

type Symbol struct {
	Name     string     `json:"name"`
	Kind     SymbolKind `json:"kind"`
	KindName string     `json:"kind_name"`
	File     string     `json:"file"`
	Line     int        `json:"line"`
	Language string     `json:"language"`
}

type Index struct {
	mu      sync.RWMutex
	Symbols []Symbol `json:"symbols"`
	Files   int      `json:"files"`
	Langs   []string `json:"languages"`
}

type Indexer struct {
	parsers    map[string]*sitter.Parser
	langForExt map[string]string
}

type queryDef struct {
	name  string
	query string
}

var goQueries = []queryDef{
	{name: "function", query: `(function_declaration name: (identifier) @name) @func`},
	{name: "method", query: `(method_declaration name: (field_identifier) @name) @method`},
	{name: "struct", query: `(type_declaration (type_spec name: (type_identifier) @name (struct_type))) @struct`},
	{name: "interface", query: `(type_declaration (type_spec name: (type_identifier) @name (interface_type))) @interface`},
	{name: "import", query: `(import_spec path: (interpreted_string_literal) @path) @import`},
}

var tsQueries = []queryDef{
	{name: "function", query: `(function_declaration name: (identifier) @name) @func`},
	{name: "class", query: `(class_declaration name: (type_identifier) @name) @class`},
	{name: "interface", query: `(interface_declaration name: (type_identifier) @name) @interface`},
	{name: "import", query: `(import_statement source: (string) @path) @import`},
}

var pyQueries = []queryDef{
	{name: "function", query: `(function_definition name: (identifier) @name) @func`},
	{name: "class", query: `(class_definition name: (identifier) @name) @class`},
	{name: "import", query: `(import_statement name: (dotted_name) @name) @import`},
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"target": true, "build": true, "dist": true, ".ok": true,
}

func NewIndexer() *Indexer {
	idx := &Indexer{
		parsers:    make(map[string]*sitter.Parser),
		langForExt: make(map[string]string),
	}

	add := func(ext, lang string, getGrammar func() *sitter.Language) {
		idx.langForExt[ext] = lang
		p := sitter.NewParser()
		p.SetLanguage(getGrammar())
		idx.parsers[lang] = p
	}

	add(".go", "go", golang.GetLanguage)
	add(".ts", "typescript", typescript.GetLanguage)
	add(".tsx", "typescript", typescript.GetLanguage)
	add(".py", "python", python.GetLanguage)

	return idx
}

func (idx *Indexer) IndexDir(ctx context.Context, root string) (*Index, error) {
	result := &Index{Langs: []string{}}
	var files []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if idx.langForExt[ext] == "" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return result, nil
	}

	result.Files = len(files)
	langSet := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for _, f := range files {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(file string) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "codegraph treesitter: panic parsing %s: %v\n", file, r)
				}
			}()
			symbols, lang := idx.parseFile(file)
			mu.Lock()
			result.Symbols = append(result.Symbols, symbols...)
			if lang != "" {
				langSet[lang] = true
			}
			mu.Unlock()
		}(f)
	}
	wg.Wait()

	for l := range langSet {
		result.Langs = append(result.Langs, l)
	}
	sort.Strings(result.Langs)

	sort.Slice(result.Symbols, func(i, j int) bool {
		if result.Symbols[i].File != result.Symbols[j].File {
			return result.Symbols[i].File < result.Symbols[j].File
		}
		return result.Symbols[i].Line < result.Symbols[j].Line
	})

	return result, nil
}

func (idx *Indexer) parseFile(path string) ([]Symbol, string) {
	ext := strings.ToLower(filepath.Ext(path))
	lang := idx.langForExt[ext]
	if lang == "" {
		return nil, ""
	}

	parser, ok := idx.parsers[lang]
	if !ok {
		return nil, ""
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, lang
	}

	tree, err := parser.ParseCtx(sitter.NewParseContext(), nil, data)
	if err != nil || tree == nil {
		return nil, lang
	}
	defer log.Close("tree-sitter", tree)

	root := tree.RootNode()
	var queries []queryDef
	switch lang {
	case "go":
		queries = goQueries
	case "typescript":
		queries = tsQueries
	case "python":
		queries = pyQueries
	}

	relPath := path
	var symbols []Symbol
	for _, qd := range queries {
		q, err := sitter.NewQuery([]byte(qd.query), parser.Language())
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
				name := cap.Node.Content(data)
				if name == "" {
					continue
				}
				line := int(cap.Node.StartPoint().Row) + 1
				symbols = append(symbols, Symbol{
					Name:     name,
					Kind:     kindFromQuery(qd.name),
					KindName: qd.name,
					File:     relPath,
					Line:     line,
					Language: lang,
				})
			}
		}
		if cerr := qc.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "treesitter: query cursor close: %v\n", cerr)
		}
		if cerr := q.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "treesitter: query close: %v\n", cerr)
		}
	}

	return symbols, lang
}

func kindFromQuery(name string) SymbolKind {
	switch name {
	case "function":
		return SymbolFunction
	case "method":
		return SymbolMethod
	case "struct":
		return SymbolStruct
	case "interface":
		return SymbolInterface
	case "class":
		return SymbolClass
	case "import":
		return SymbolImport
	default:
		return SymbolVariable
	}
}

func (idx *Index) Summarize() string {
	if len(idx.Symbols) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Knowledge\n\n")
	fmt.Fprintf(&b, "Languages: %s\n", strings.Join(idx.Langs, ", "))
	fmt.Fprintf(&b, "Files indexed: %d\n", idx.Files)

	top := 0
	for _, s := range idx.Symbols {
		if top >= 30 {
			break
		}
		switch s.Kind {
		case SymbolFunction, SymbolStruct, SymbolInterface, SymbolClass:
			b.WriteString(fmt.Sprintf("  - %s %s (%s:%d)\n", s.KindName, s.Name, s.File, s.Line))
			top++
		}
	}
	return b.String()
}
