//go:build !treesitter

package semantic

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// skipDirsRegex lists directories excluded from chunking in regex mode.
var skipDirsRegex = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"target": true, "build": true, "dist": true,
	".ok": true, ".codegraph": true,
}

// ChunkDir walks root and extracts code chunks from recognized source files.
func (c *Chunker) ChunkDir(root string) ([]Chunk, error) {
	var chunks []Chunk

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable files
		}
		if info.IsDir() {
			if skipDirsRegex[info.Name()] || strings.HasPrefix(info.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".go":
			chunks = append(chunks, chunkGoFileRegex(path, root)...)
		case ".ts", ".tsx":
			chunks = append(chunks, chunkTSFileRegex(path, root)...)
		case ".py":
			chunks = append(chunks, chunkPyFileRegex(path, root)...)
		case ".rs":
			chunks = append(chunks, chunkRsFileRegex(path, root)...)
		default: // unsupported language — skip
		}
		return nil
	})

	return chunks, err
}

var goFuncRe = regexp.MustCompile(`(?m)^func\s+(\(\w+\s+\*?\w+\)\s+)?(\w+)\s*\([^)]*\)`)
var goMethodRe = regexp.MustCompile(`(?m)^func\s+\(\w+\s+\*?(\w+)\)\s+(\w+)\s*\([^)]*\)`)
var goTypeRe = regexp.MustCompile(`(?m)^type\s+(\w+)\s+(struct|interface)\s*\{`)
var goTypeAliasRe = regexp.MustCompile(`(?m)^type\s+(\w+)\s+(?:func|\[|map|chan|\*)`)

func chunkGoFileRegex(path, root string) []Chunk {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := string(data)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	lines := strings.Split(s, "\n")
	var chunks []Chunk

	for _, re := range []struct {
		re   *regexp.Regexp
		kind string
	}{
		{goFuncRe, "function"},
		{goMethodRe, "method"},
	} {
		for _, m := range re.re.FindAllStringSubmatch(s, -1) {
			name := m[len(m)-1]
			if name == "" || isGoKeyword(name) {
				continue
			}
			line := findLine(lines, m[0])
			doc := extractDoc(lines, line)
			chunks = append(chunks, Chunk{
				ID: fmtID(rel, line, re.kind, name), File: rel, Line: line,
				Content: m[0], Language: "go", Kind: re.kind, Doc: doc,
			})
		}
	}
	for _, m := range goTypeRe.FindAllStringSubmatch(s, -1) {
		line := findLine(lines, m[0])
		chunks = append(chunks, Chunk{
			ID: fmtID(rel, line, "type", m[1]), File: rel, Line: line,
			Content: m[0], Language: "go", Kind: "type", Doc: extractDoc(lines, line),
		})
	}
	for _, m := range goTypeAliasRe.FindAllStringSubmatch(s, -1) {
		line := findLine(lines, m[0])
		chunks = append(chunks, Chunk{
			ID: fmtID(rel, line, "type", m[1]), File: rel, Line: line,
			Content: m[0], Language: "go", Kind: "type", Doc: extractDoc(lines, line),
		})
	}
	return chunks
}

var tsFuncRe = regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?function\s+(\w+)\s*\([^)]*\)`)
var tsClassRe = regexp.MustCompile(`(?m)^(?:export\s+)?class\s+(\w+)`)
var tsInterfaceRe = regexp.MustCompile(`(?m)^(?:export\s+)?interface\s+(\w+)`)

func chunkTSFileRegex(path, root string) []Chunk {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := string(data)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	lines := strings.Split(s, "\n")
	var chunks []Chunk
	for _, re := range []struct {
		re   *regexp.Regexp
		kind string
	}{
		{tsFuncRe, "function"}, {tsClassRe, "class"}, {tsInterfaceRe, "interface"},
	} {
		for _, m := range re.re.FindAllStringSubmatch(s, -1) {
			line := findLine(lines, m[0])
			chunks = append(chunks, Chunk{
				ID: fmtID(rel, line, re.kind, m[1]), File: rel, Line: line,
				Content: m[0], Language: "typescript", Kind: re.kind,
			})
		}
	}
	return chunks
}

var pyFuncRe = regexp.MustCompile(`(?m)^def\s+(\w+)\s*\(`)
var pyClassRe = regexp.MustCompile(`(?m)^class\s+(\w+)`)

func chunkPyFileRegex(path, root string) []Chunk {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := string(data)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	lines := strings.Split(s, "\n")
	var chunks []Chunk
	for _, re := range []struct {
		re   *regexp.Regexp
		kind string
	}{
		{pyFuncRe, "function"}, {pyClassRe, "class"},
	} {
		for _, m := range re.re.FindAllStringSubmatch(s, -1) {
			line := findLine(lines, m[0])
			chunks = append(chunks, Chunk{
				ID: fmtID(rel, line, re.kind, m[1]), File: rel, Line: line,
				Content: m[0], Language: "python", Kind: re.kind,
			})
		}
	}
	return chunks
}

var rsFuncRe = regexp.MustCompile(`(?m)^(?:pub\s+)?fn\s+(\w+)\s*[<\(]`)
var rsStructRe = regexp.MustCompile(`(?m)^(?:pub\s+)?struct\s+(\w+)`)
var rsEnumRe = regexp.MustCompile(`(?m)^(?:pub\s+)?enum\s+(\w+)`)

func chunkRsFileRegex(path, root string) []Chunk {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := string(data)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	lines := strings.Split(s, "\n")
	var chunks []Chunk
	for _, re := range []struct {
		re   *regexp.Regexp
		kind string
	}{
		{rsFuncRe, "function"}, {rsStructRe, "struct"}, {rsEnumRe, "enum"},
	} {
		for _, m := range re.re.FindAllStringSubmatch(s, -1) {
			line := findLine(lines, m[0])
			chunks = append(chunks, Chunk{
				ID: fmtID(rel, line, re.kind, m[1]), File: rel, Line: line,
				Content: m[0], Language: "rust", Kind: re.kind,
			})
		}
	}
	return chunks
}

func findLine(lines []string, match string) int {
	for i, line := range lines {
		if strings.Contains(line, match) {
			return i + 1
		}
	}
	return 0
}

func extractDoc(lines []string, line int) string {
	if line < 2 {
		return ""
	}
	var docs []string
	for i := line - 2; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "//") {
			docs = append(docs, strings.TrimPrefix(trimmed, "// "))
		} else if trimmed == "" {
			continue
		} else {
			break
		}
	}
	// Reverse: we collected in reverse order (bottom-up).
	for i, j := 0, len(docs)-1; i < j; i, j = i+1, j-1 {
		docs[i], docs[j] = docs[j], docs[i]
	}
	return strings.Join(docs, " ")
}

func fmtID(file string, line int, kind, name string) string {
	return file + ":" + itoa(line) + ":" + kind + ":" + name
}

func itoa(n int) string { return strconv.Itoa(n) }

var goKeywords = map[string]bool{
	"if": true, "for": true, "return": true, "nil": true,
	"true": true, "false": true, "break": true, "continue": true,
	"const": true, "var": true, "switch": true, "case": true,
	"default": true, "defer": true, "go": true, "select": true,
	"range": true, "map": true, "chan": true, "func": true,
	"type": true, "interface": true, "struct": true, "package": true,
	"import": true,
}

func isGoKeyword(s string) bool { return goKeywords[s] }
