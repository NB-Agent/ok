package semantic

import (
	"os"
	"path/filepath"
	"strings"
)

// ChunkMemoryDir reads markdown files from dir (typically .ok/memory/) and
// returns them as individual chunks, each a whole file with its heading as doc.
// Returns nil if the directory does not exist.
//
// This method is shared by both the regex and tree-sitter chunker implementations
// since it handles markdown files, not source code.
func (c *Chunker) ChunkMemoryDir(dir string) []Chunk {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var chunks []Chunk
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			continue
		}
		content := string(data)
		doc := ""
		lines := strings.SplitN(content, "\n", 3)
		if len(lines) > 0 && strings.HasPrefix(lines[0], "#") {
			doc = strings.TrimPrefix(lines[0], "# ")
		}
		chunks = append(chunks, Chunk{
			ID:       "memory:" + e.Name(),
			File:     filepath.Join(".ok/memory", e.Name()),
			Line:     1,
			Content:  content,
			Language: "markdown",
			Kind:     "memory",
			Doc:      doc,
		})
	}
	return chunks
}
