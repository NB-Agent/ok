package semantic

// Chunk is a code fragment suitable for embedding and search.
type Chunk struct {
	ID       string `json:"id"`       // e.g. "file.go:42:func:DoThing"
	File     string `json:"file"`     // relative path
	Line     int    `json:"line"`     // 1-based line number
	Content  string `json:"content"`  // the code text (signature + body)
	Language string `json:"language"` // "go", "typescript", "python", "rust"
	Kind     string `json:"kind"`     // "function", "method", "struct", "interface", "class", "enum"
	Doc      string `json:"doc"`      // preceding comment, if any
}

// Chunker extracts code chunks from source files.
// The concrete implementation is selected at build time:
//   - chunker_treesitter.go (build tag: treesitter) — AST-level extraction via tree-sitter
//   - chunker_regex.go    (build tag: !treesitter) — regex fallback
type Chunker struct{}

// NewChunker creates a chunker (implementation chosen at build time).
func NewChunker() *Chunker { return &Chunker{} }
