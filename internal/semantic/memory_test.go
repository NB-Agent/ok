package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChunkMemoryDir_SkipsEmpty(t *testing.T) {
	c := NewChunker()
	chunks := c.ChunkMemoryDir("/nonexistent")
	if chunks != nil {
		t.Errorf("expected nil for nonexistent dir, got %d chunks", len(chunks))
	}
}

func TestChunkMemoryDir_ReadsMarkdownFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.md"), []byte("# Hello\n\nThis is a test memory document.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewChunker()
	chunks := c.ChunkMemoryDir(dir)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Content, "Hello") {
		t.Errorf("chunk content missing 'Hello': %s", chunks[0].Content)
	}
	if chunks[0].Doc != "Hello" {
		t.Errorf("doc = %q, want 'Hello'", chunks[0].Doc)
	}
	if chunks[0].Language != "markdown" {
		t.Errorf("language = %q, want 'markdown'", chunks[0].Language)
	}
	if chunks[0].Kind != "memory" {
		t.Errorf("kind = %q, want 'memory'", chunks[0].Kind)
	}
}

func TestChunkMemoryDir_SkipsNonMarkdown(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "note.txt"), []byte("plain text"), 0o644)
	os.WriteFile(filepath.Join(dir, "data.bin"), []byte{0, 1, 2}, 0o644)

	c := NewChunker()
	chunks := c.ChunkMemoryDir(dir)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for non-md files, got %d", len(chunks))
	}
}

func TestEngine_HasMemoryDir(t *testing.T) {
	e := NewEngine(t.TempDir())
	if e.memoryDir == "" {
		t.Error("memoryDir should be set by NewEngine")
	}
	if !strings.Contains(e.memoryDir, "memory") {
		t.Errorf("memoryDir should contain 'memory', got %s", e.memoryDir)
	}
}

func TestIndexRemoveByKind(t *testing.T) {
	idx := NewIndex("")
	idx.Add(Chunk{ID: "a", Kind: "function", Content: "func foo()"}, []float32{1, 0})
	idx.Add(Chunk{ID: "b", Kind: "memory", Content: "# Memory doc"}, []float32{0, 1})
	idx.Add(Chunk{ID: "c", Kind: "function", Content: "func bar()"}, []float32{1, 1})
	idx.Add(Chunk{ID: "d", Kind: "memory", Content: "# Another memory"}, []float32{0, 0})

	idx.RemoveByKind("memory")
	if idx.Size() != 2 {
		t.Errorf("expected 2 entries after removing memory, got %d", idx.Size())
	}

	// Remaining should be a and c. Keyword search by content.
	results := idx.KeywordSearch("func", 10)
	if len(results) != 2 {
		t.Errorf("expected 2 keyword results for 'func', got %d", len(results))
	}
}

func TestIndexRemoveByKind_AllGone(t *testing.T) {
	idx := NewIndex("")
	idx.Add(Chunk{ID: "x", Kind: "memory", Content: "# Only memory"}, []float32{1, 0})
	idx.RemoveByKind("memory")
	if idx.Size() != 0 {
		t.Errorf("expected 0 entries, got %d", idx.Size())
	}
}
