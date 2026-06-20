package semantic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NB-Agent/ok/internal/log"
)

// Engine ties together embedding, chunking, storage, and search.
type Engine struct {
	embedder *Embedder
	chunker  *Chunker
	index    *Index

	projectRoot string
	memoryDir   string
	modelName   string

	mu       sync.Mutex
	indexing bool // true while a background index is in progress
	ready    bool // true when at least one indexing pass completed
	wg       sync.WaitGroup
}

// NewEngine creates a semantic search engine for the given project root.
func NewEngine(projectRoot string) *Engine {
	embedder := NewEmbedder("http://localhost:11434", "nomic-embed-text")
	indexPath := filepath.Join(projectRoot, ".ok", "semantic-index.json")
	memoryDir := filepath.Join(projectRoot, ".ok", "memory")
	return &Engine{
		embedder:    embedder,
		chunker:     NewChunker(),
		index:       NewIndex(indexPath),
		projectRoot: projectRoot,
		memoryDir:   memoryDir,
		modelName:   "nomic-embed-text",
	}
}

// Healthy reports whether Ollama is reachable.
func (e *Engine) Healthy(ctx context.Context) bool {
	return e.embedder.Healthy(ctx)
}

// IndexSize returns the number of indexed chunks.
func (e *Engine) IndexSize() int { return e.index.Size() }

// IsReady reports whether the index has been built at least once.
func (e *Engine) IsReady() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ready
}

// BuildIndex synchronously extracts chunks, embeds them, and stores results.
// This can take several minutes on large codebases. For background indexing,
// use BuildIndexAsync.
func (e *Engine) BuildIndex(ctx context.Context) error {
	e.mu.Lock()
	if e.indexing {
		e.mu.Unlock()
		return fmt.Errorf("indexing already in progress")
	}
	e.indexing = true
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.indexing = false
		e.ready = true
		e.mu.Unlock()
	}()

	// Try to load existing index first
	if err := e.index.Load(); err == nil && e.index.Size() > 0 {
		return nil // already indexed
	}

	// Extract chunks
	chunks, err := e.chunker.ChunkDir(e.projectRoot)
	if err != nil {
		return fmt.Errorf("chunking: %w", err)
	}

	// Also index memory documents if the directory exists.
	if memChunks := e.chunker.ChunkMemoryDir(e.memoryDir); len(memChunks) > 0 {
		chunks = append(chunks, memChunks...)
	}

	if len(chunks) == 0 {
		return fmt.Errorf("no code chunks found in %s", e.projectRoot)
	}

	// Embed in batches (10 at a time, Ollama can handle small batches)
	batchSize := 10
	for i := 0; i < len(chunks); i += batchSize {
		end := i + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		batch := chunks[i:end]

		// Build text for embedding: doc comment + function signature
		texts := make([]string, len(batch))
		for j, c := range batch {
			text := c.Content
			if c.Doc != "" {
				text = c.Doc + "\n" + text
			}
			texts[j] = text
		}

		// Embed each text individually (Ollama API doesn't support batch)
		for j, t := range texts {
			vec, err := e.embedder.Embed(ctx, t)
			if err != nil {
				fmt.Fprintf(os.Stderr, "semantic: embed failed: %v\n", err)
				continue
			}
			if vec != nil {
				e.index.Add(batch[j], vec)
			}
		}
	}

	// Persist
	if err := e.index.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "semantic: failed to save index: %v\n", err)
	}

	return nil
}

// RebuildMemoryIndex re-embeds only the memory chunks and saves the index.
// Call this after a memory document is added or updated at runtime.
func (e *Engine) RebuildMemoryIndex(ctx context.Context) error {
	memChunks := e.chunker.ChunkMemoryDir(e.memoryDir)
	if len(memChunks) == 0 {
		return nil
	}

	// Load existing index first (code chunks).
	if err := e.index.Load(); err != nil {
		// If no existing index, do a full build instead.
		return e.BuildIndex(ctx)
	}

	// Remove old memory entries, keep code entries.
	e.index.RemoveByKind("memory")

	// Embed and add memory chunks.
	for i := range memChunks {
		text := memChunks[i].Content
		if memChunks[i].Doc != "" {
			text = memChunks[i].Doc + "\n" + text
		}
		vec, err := e.embedder.Embed(ctx, text)
		if err != nil {
			fmt.Fprintf(os.Stderr, "semantic: embed failed: %v\n", err)
			continue
		}
		if vec != nil {
			e.index.Add(memChunks[i], vec)
		}
	}

	return e.index.Save()
}

// BuildIndexAsync starts indexing in the background. Returns immediately.
// Progress is reflected in IndexSize() and IsReady().
func (e *Engine) BuildIndexAsync() {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Error("goroutine panic", "recover", r)
				fmt.Fprintf(os.Stderr, "semantic: panic in background index: %v\n", r)
			}
		}()
		// Background indexing runs independently; no parent ctx to inherit.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := e.BuildIndex(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "semantic: background index failed: %v\n", err)
		}
	}()
}

// Shutdown waits for any background indexing to complete, then releases
// resources. Call before process exit to avoid goroutine leaks.
func (e *Engine) Shutdown() {
	e.wg.Wait()
}

// Search performs a semantic + keyword hybrid search.
// Falls back to keyword-only if the embedder is not healthy.
func (e *Engine) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 10
	}

	// Try semantic search
	if e.embedder.Healthy(ctx) {
		vec, err := e.embedder.Embed(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("semantic search: %w", err)
		}
		if vec != nil {
			return e.index.HybridSearch(query, vec, topK), nil
		}
	}

	// Fall back to keyword-only
	return e.index.KeywordSearch(query, topK), nil
}

// FormatResults returns a human-readable summary of search results.
func FormatResults(query string, results []SearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("# Semantic Search: \"%s\"\n\nNo results found.", query)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Semantic Search: \"%s\"\n\n", query))
	b.WriteString(fmt.Sprintf("Found %d result(s):\n\n", len(results)))

	for i, r := range results {
		lang := r.Chunk.Language
		if lang == "" {
			lang = "text"
		}
		b.WriteString(fmt.Sprintf("## %d. %s (%.2f) [%s]\n", i+1, r.Chunk.ID, r.Score, r.MatchType))
		b.WriteString(fmt.Sprintf("```%s\n%s\n```\n", lang, r.Chunk.Content))
		if r.Chunk.Doc != "" {
			b.WriteString(fmt.Sprintf("> %s\n", r.Chunk.Doc))
		}
		b.WriteString("\n")
	}

	return b.String()
}
