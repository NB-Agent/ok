// Package codegraph extracts code symbols using pure Go regex.
// No CGO, no tree-sitter, no external dependencies — works everywhere.
// Covers Go, TypeScript/JavaScript, Python, Rust, Java, C, C++.
//
//go:build !treesitter

package codegraph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
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
	SymbolEnum
	SymbolTrait
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
	Symbols []Symbol `json:"symbols"`
	Files   int      `json:"files"`
	Langs   []string `json:"languages"`
}

// langPatterns maps file extensions to language names and regex patterns.
type langPatterns struct {
	lang     string
	ext      string
	matchers []symbolMatcher
}

type symbolMatcher struct {
	kind SymbolKind
	name string
	re   *regexp.Regexp
}

var patterns = buildPatterns()

func buildPatterns() []langPatterns {
	return []langPatterns{
		{
			lang: "go", ext: ".go",
			matchers: []symbolMatcher{
				{SymbolFunction, "function", regexp.MustCompile(`func\s+(\(\w+\s+\*?\w+\)\s+)?(\w+)\s*\(`)},
				{SymbolMethod, "method", regexp.MustCompile(`func\s+\(\w+\s+\*?\w+\)\s+(\w+)\s*\(`)},
				{SymbolStruct, "struct", regexp.MustCompile(`type\s+(\w+)\s+struct`)},
				{SymbolInterface, "interface", regexp.MustCompile(`type\s+(\w+)\s+interface`)},
				{SymbolType, "type", regexp.MustCompile(`type\s+(\w+)\s+(?:func|interface|struct|\[|map|chan|\*|~)`)},
				{SymbolImport, "import", regexp.MustCompile(`"([^"]+)"`)},
			},
		},
		{
			lang: "typescript", ext: ".ts",
			matchers: []symbolMatcher{
				{SymbolFunction, "function", regexp.MustCompile(`(?:export\s+)?(?:async\s+)?function\s+(\w+)\s*\(`)},
				{SymbolMethod, "method", regexp.MustCompile(`(?:async\s+)?(\w+)\s*\(\s*(?:\)\s*:\s*\w+\s*)?\{`)},
				{SymbolClass, "class", regexp.MustCompile(`(?:export\s+)?class\s+(\w+)`)},
				{SymbolInterface, "interface", regexp.MustCompile(`(?:export\s+)?interface\s+(\w+)`)},
				{SymbolType, "type", regexp.MustCompile(`(?:export\s+)?type\s+(\w+)\s*=`)},
				{SymbolImport, "import", regexp.MustCompile(`(?:import\s+.*?from\s+)?["']([^"']+)["']`)},
			},
		},
		{
			lang: "typescript", ext: ".tsx",
			matchers: nil, // inherited from .ts patterns
		},
		{
			lang: "javascript", ext: ".js",
			matchers: []symbolMatcher{
				{SymbolFunction, "function", regexp.MustCompile(`(?:export\s+)?(?:async\s+)?function\s+(\w+)\s*\(`)},
				{SymbolClass, "class", regexp.MustCompile(`(?:export\s+)?class\s+(\w+)`)},
				{SymbolImport, "import", regexp.MustCompile(`(?:import\s+|require\(|[md]\(?)["']([^"']+)["']`)},
			},
		},
		{
			lang: "python", ext: ".py",
			matchers: []symbolMatcher{
				{SymbolFunction, "function", regexp.MustCompile(`def\s+(\w+)\s*\(`)},
				{SymbolClass, "class", regexp.MustCompile(`class\s+(\w+)\s*[:(]`)},
				{SymbolImport, "import", regexp.MustCompile(`(?:from|import)\s+(\w+)`)},
			},
		},
		{
			lang: "rust", ext: ".rs",
			matchers: []symbolMatcher{
				{SymbolFunction, "function", regexp.MustCompile(`fn\s+(\w+)\s*[<\(]`)},
				{SymbolStruct, "struct", regexp.MustCompile(`struct\s+(\w+)`)},
				{SymbolEnum, "enum", regexp.MustCompile(`enum\s+(\w+)`)},
				{SymbolTrait, "trait", regexp.MustCompile(`trait\s+(\w+)`)},
				{SymbolImport, "import", regexp.MustCompile(`use\s+([\w:]+)`)},
			},
		},
		{
			lang: "java", ext: ".java",
			matchers: []symbolMatcher{
				{SymbolMethod, "method", regexp.MustCompile(`(?:public|private|protected|static|\s)+\s+(\w+)\s+(\w+)\s*\(`)},
				{SymbolClass, "class", regexp.MustCompile(`class\s+(\w+)`)},
				{SymbolInterface, "interface", regexp.MustCompile(`interface\s+(\w+)`)},
				{SymbolImport, "import", regexp.MustCompile(`import\s+([\w.]+)`)},
			},
		},
		{
			lang: "c", ext: ".c",
			matchers: []symbolMatcher{
				{SymbolFunction, "function", regexp.MustCompile(`(\w+)\s+(\w+)\s*\([^)]*\)\s*\{`)},
				{SymbolImport, "include", regexp.MustCompile(`#include\s+[<"]([^>"]+)[>"]`)},
			},
		},
		{
			lang: "cpp", ext: ".cpp",
			matchers: []symbolMatcher{
				{SymbolFunction, "function", regexp.MustCompile(`(?:\w[\w:<>*&]*\s+)+(\w+)\s*\([^)]*\)\s*(?:const\s*)?\{`)},
				{SymbolClass, "class", regexp.MustCompile(`class\s+(\w+)`)},
				{SymbolStruct, "struct", regexp.MustCompile(`struct\s+(\w+)`)},
				{SymbolImport, "include", regexp.MustCompile(`#include\s+[<"]([^>"]+)[>"]`)},
			},
		},
		{
			lang: "cpp", ext: ".h",
			matchers: []symbolMatcher{
				{SymbolFunction, "function", regexp.MustCompile(`(\w[\w:<>*&]*\s+)+(\w+)\s*\([^)]*\)\s*;`)},
				{SymbolClass, "class", regexp.MustCompile(`class\s+(\w+)`)},
				{SymbolStruct, "struct", regexp.MustCompile(`struct\s+(\w+)`)},
			},
		},
	}
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"target": true, "build": true, "dist": true, ".ok": true,
}

// patternByExt returns the patterns for a file extension, caching the lookup.
var patternByExt = func() map[string]*langPatterns {
	m := make(map[string]*langPatterns)
	for i := range patterns {
		lp := &patterns[i]
		m[lp.ext] = lp
	}
	// TSX inherits from TS patterns
	ts := m[".ts"]
	m[".tsx"] = &langPatterns{lang: "typescript", ext: ".tsx", matchers: ts.matchers}
	return m
}()

func NewIndexer() *Indexer {
	return &Indexer{}
}

type Indexer struct{}

func (idx *Indexer) IndexDir(ctx context.Context, root string) (*Index, error) {
	result := &Index{Langs: []string{}}
	extMap := patternByExt

	var files []string
	var langs []string
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable files
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if lp, ok := extMap[ext]; ok {
			files = append(files, path)
			langs = append(langs, lp.lang)
		}
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "codegraph regex: walk %s: %v\n", root, err)
	}

	result.Files = len(files)
	langSet := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for i, f := range files {
		select {
		case <-ctx.Done():
			wg.Wait() // drain in-flight goroutines before returning to avoid data race
			return result, ctx.Err()
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(file, lang string) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "codegraph regex: panic parsing %s: %v\n", file, r)
				}
			}()
			symbols := parseFile(file, lang)
			mu.Lock()
			result.Symbols = append(result.Symbols, symbols...)
			if lang != "" {
				langSet[lang] = true
			}
			mu.Unlock()
		}(f, langs[i])
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

func parseFile(path string, lang string) []Symbol {
	ext := strings.ToLower(filepath.Ext(path))
	lp := patternByExt[ext]
	if lp == nil {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	var symbols []Symbol

	for _, m := range lp.matchers {
		for lineNum, line := range lines {
			matches := m.re.FindAllStringSubmatch(line, -1)
			for _, match := range matches {
				// Find the capture group that holds the symbol name.
				// For most patterns it's the first group, for method patterns
				// on Go it's the method name capture.
				name := ""
				for j := 1; j < len(match); j++ {
					if match[j] != "" && len(match[j]) < 100 {
						if name == "" || isSymbolName(match[j]) {
							name = match[j]
						}
					}
				}
				if name == "" || isKeyword(name) {
					continue
				}

				symbols = append(symbols, Symbol{
					Name:     name,
					Kind:     m.kind,
					KindName: m.name,
					File:     path,
					Line:     lineNum + 1,
					Language: lang,
				})
			}
		}
	}
	return symbols
}

// isSymbolName checks if a string looks like a valid identifier.
func isSymbolName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, ch := range s {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '_' {
			return false
		}
	}
	return s[0] >= 'A' && s[0] <= 'Z' || s[0] >= 'a' && s[0] <= 'z' || s[0] == '_'
}

// isKeyword filters out language keywords that accidentally match.
func isKeyword(s string) bool {
	kw := map[string]bool{
		"if": true, "for": true, "return": true, "nil": true,
		"true": true, "false": true, "break": true, "continue": true,
		"const": true, "var": true, "switch": true, "case": true,
		"default": true, "defer": true, "go": true, "select": true,
		"range": true, "map": true, "chan": true,
		"new": true, "make": true, "else": true, "error": true,
		"int": true, "string": true, "bool": true, "float64": true,
		"byte": true, "rune": true, "any": true, "interface": true,
		"async": true, "await": true, "export": true, "from": true,
		"import": true, "def": true, "class": true, "fn": true,
		"struct": true, "enum": true, "trait": true, "impl": true,
		"pub": true, "priv": true, "mod": true, "use": true,
		"type": true, "void": true, "char": true, "long": true,
		"short": true, "unsigned": true, "signed": true, "auto": true,
		"static": true, "extern": true, "volatile": true, "register": true,
	}
	return kw[s]
}

// Summarize returns a concise string for system prompt injection.
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
		case SymbolFunction, SymbolStruct, SymbolInterface, SymbolClass, SymbolMethod:
			b.WriteString(fmt.Sprintf("  - %s %s (%s:%d)\n", s.KindName, s.Name, s.File, s.Line))
			top++
		default: // unknown — ignore
		}
	}
	return b.String()
}
