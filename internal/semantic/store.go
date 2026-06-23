package semantic

import (
	"container/heap"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Index stores code chunks and their embeddings, supporting cosine-similarity
// search and JSON persistence.
type Index struct {
	mu      sync.RWMutex `json:"-"`
	entries []indexEntry
	path    string // persisted JSON file path
}

type indexEntry struct {
	Chunk     Chunk     `json:"chunk"`
	Embedding []float32 `json:"embedding"`
}

// SearchResult is a single match from a vector search.
type SearchResult struct {
	Chunk     Chunk   `json:"chunk"`
	Score     float32 `json:"score"`     // 0–1, higher = more similar
	MatchType string  `json:"matchType"` // "semantic" or "keyword"
}

// NewIndex creates an index with optional persistence path.
// If persistPath is "", the index is memory-only.
func NewIndex(persistPath string) *Index {
	return &Index{path: persistPath}
}

// Size returns the number of indexed chunks.
func (idx *Index) Size() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries)
}

// Add appends a chunk with its embedding to the index.
func (idx *Index) Add(chunk Chunk, embedding []float32) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.entries = append(idx.entries, indexEntry{
		Chunk:     chunk,
		Embedding: embedding,
	})
}

// AddBatch appends multiple chunks with embeddings.
func (idx *Index) AddBatch(chunks []Chunk, embeddings [][]float32) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for i, c := range chunks {
		if i < len(embeddings) {
			idx.entries = append(idx.entries, indexEntry{
				Chunk:     c,
				Embedding: embeddings[i],
			})
		}
	}
}

// Clear removes all entries from the index.
func (idx *Index) Clear() {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.entries = nil
}

// RemoveByKind removes all entries with the given chunk Kind (e.g. "memory").
func (idx *Index) RemoveByKind(kind string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	filtered := make([]indexEntry, 0, len(idx.entries))
	for _, e := range idx.entries {
		if e.Chunk.Kind != kind {
			filtered = append(filtered, e)
		}
	}
	idx.entries = filtered
}

// scoreHeap implements container/heap for top-K selection.
type scoreHeap struct {
	entries []scoredEntry
	topK    int
}

type scoredEntry struct {
	entry indexEntry
	score float32
}

func (h *scoreHeap) Len() int           { return len(h.entries) }
func (h *scoreHeap) Less(i, j int) bool { return h.entries[i].score < h.entries[j].score } // min-heap
func (h *scoreHeap) Swap(i, j int)      { h.entries[i], h.entries[j] = h.entries[j], h.entries[i] }
func (h *scoreHeap) Push(x interface{}) {
	if s, ok := x.(scoredEntry); ok {
		h.entries = append(h.entries, s)
	}
}
func (h *scoreHeap) Pop() interface{} {
	old := h.entries
	n := len(old)
	x := old[n-1]
	h.entries = old[:n-1]
	return x
}

// Search finds the top-k chunks most similar to the query embedding.
// Returns results sorted by descending cosine similarity.
func (idx *Index) Search(queryVec []float32, topK int) []SearchResult {
	if topK <= 0 {
		topK = 10
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(idx.entries) == 0 {
		return nil
	}

	// Use min-heap for O(N log topK) instead of full O(N log N) sort.
	h := &scoreHeap{topK: topK}
	for _, e := range idx.entries {
		score := cosineSim(queryVec, e.Embedding)
		if h.Len() < topK {
			heap.Push(h, scoredEntry{entry: e, score: score})
		} else if score > h.entries[0].score {
			h.entries[0] = scoredEntry{entry: e, score: score}
			heap.Fix(h, 0)
		}
	}

	// Extract results in descending order.
	n := h.Len()
	results := make([]SearchResult, n)
	for i := n - 1; i >= 0; i-- {
		s, ok := heap.Pop(h).(scoredEntry)
		if !ok {
			continue
		}
		results[i] = SearchResult{
			Chunk:     s.entry.Chunk,
			Score:     s.score,
			MatchType: "semantic",
		}
	}
	return results
}

// KeywordSearch finds chunks whose content/doc/file contains the query string.
func (idx *Index) KeywordSearch(query string, topK int) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if topK <= 0 {
		topK = 10
	}

	queryLower := strings.ToLower(query)

	// Pre-allocate with capacity of topK — typical search has few matches.
	matches := make([]scoredEntry, 0, topK)
	// Track the minimum score in matches for early rejection.
	minMatchScore := float32(0)

	for _, e := range idx.entries {
		// Lower once per entry to avoid repeated allocations.
		contentLow := strings.ToLower(e.Chunk.Content)
		docLow := strings.ToLower(e.Chunk.Doc)
		fileLow := strings.ToLower(e.Chunk.File)

		count := 0
		if strings.Contains(e.Chunk.Content, query) || strings.Contains(contentLow, queryLower) {
			count += 3 // content match is strongest
		}
		if count == 3 { // already content-matched
			if strings.Contains(e.Chunk.Doc, query) || strings.Contains(docLow, queryLower) {
				count += 2
			}
			if strings.Contains(e.Chunk.File, query) || strings.Contains(fileLow, queryLower) {
				count += 1
			}
		} else {
			if strings.Contains(docLow, queryLower) {
				count += 2
			}
			if strings.Contains(fileLow, queryLower) {
				count += 1
			}
		}

		if count == 0 {
			continue
		}

		score := float32(count) / 6.0

		if len(matches) < topK {
			matches = append(matches, scoredEntry{entry: e, score: score})
			if score < minMatchScore || len(matches) == 1 {
				minMatchScore = score
			}
		} else if score > minMatchScore {
			// Replace the lowest-scoring match.
			replaceIdx := 0
			for i := 1; i < len(matches); i++ {
				if matches[i].score < matches[replaceIdx].score {
					replaceIdx = i
				}
			}
			matches[replaceIdx] = scoredEntry{entry: e, score: score}
			// Recompute min.
			minMatchScore = matches[0].score
			for i := 1; i < len(matches); i++ {
				if matches[i].score < minMatchScore {
					minMatchScore = matches[i].score
				}
			}
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})

	results := make([]SearchResult, len(matches))
	for i, m := range matches {
		results[i] = SearchResult{
			Chunk:     m.entry.Chunk,
			Score:     m.score,
			MatchType: "keyword",
		}
	}
	return results
}

// HybridSearch combines semantic + keyword results with reciprocal rank fusion (RRF).
func (idx *Index) HybridSearch(query string, queryVec []float32, topK int) []SearchResult {
	semResults := idx.Search(queryVec, topK*2)
	kwResults := idx.KeywordSearch(query, topK*2)

	// RRF: score = sum of 1/(k+rank) for each result list
	const rrfK = 60.0
	merged := make(map[string]*SearchResult, topK*2)
	seen := make(map[string]int, topK*2)

	for i, r := range semResults {
		rrf := 1.0 / (rrfK + float64(i+1))
		merged[r.Chunk.ID] = &SearchResult{
			Chunk:     r.Chunk,
			Score:     float32(rrf),
			MatchType: "semantic",
		}
		seen[r.Chunk.ID] = i
	}

	for i, r := range kwResults {
		rrf := 1.0 / (rrfK + float64(i+1))
		if existing, ok := merged[r.Chunk.ID]; ok {
			existing.Score += float32(rrf)
			existing.MatchType = "hybrid"
		} else {
			merged[r.Chunk.ID] = &SearchResult{
				Chunk:     r.Chunk,
				Score:     float32(rrf),
				MatchType: "keyword",
			}
		}
	}

	// Convert map to slice and sort
	results := make([]SearchResult, 0, len(merged))
	for _, r := range merged {
		results = append(results, *r)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if topK > len(results) {
		topK = len(results)
	}
	return results[:topK]
}

// Save persists the index to disk as JSON.
func (idx *Index) Save() error {
	if idx.path == "" {
		return fmt.Errorf("no persist path configured")
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	dir := filepath.Dir(idx.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	f, err := os.Create(idx.path + ".tmp")
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			f.Close()
		}
		// Remove temp file if rename fails.
		if err != nil {
			os.Remove(idx.path + ".tmp")
		}
	}()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err = enc.Encode(struct {
		Entries []indexEntry `json:"entries"`
	}{Entries: idx.entries}); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	return os.Rename(idx.path+".tmp", idx.path)
}

// Load reads the index from disk.
func (idx *Index) Load() error {
	if idx.path == "" {
		return fmt.Errorf("no persist path configured")
	}

	data, err := os.ReadFile(idx.path)
	if err != nil {
		return err
	}

	var wrapper struct {
		Entries []indexEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return err
	}

	idx.mu.Lock()
	idx.entries = wrapper.Entries
	idx.mu.Unlock()
	return nil
}

// cosineSim returns the cosine similarity between two vectors.
// Returns 0 for zero-length vectors or length mismatch.
func cosineSim(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}
