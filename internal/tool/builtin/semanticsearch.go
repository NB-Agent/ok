package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/NB-Agent/ok/internal/semantic"
	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(semanticSearch{}) }

var (
	semanticEngine *semantic.Engine
	semanticOnce   sync.Once
)

// SetSemanticEngine installs a semantic engine for the semantic-search tool.
// Called during boot. If nil, the tool reports "not configured".
func SetSemanticEngine(eng *semantic.Engine) {
	semanticOnce.Do(func() {
		semanticEngine = eng
	})
}

// semanticSearch finds code by meaning, not just by text match.
type semanticSearch struct{}

func (semanticSearch) Name() string { return "semantic-search" }

func (semanticSearch) Description() string {
	return "Search code by meaning using semantic vector search. Finds conceptually related code even without keyword matches."
}

func (semanticSearch) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"query":{"type":"string"},"top_k":{"type":"integer"}},"required":["query"],"type":"object"}`)
}

func (semanticSearch) ReadOnly() bool { return true }

func (semanticSearch) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if semanticEngine == nil {
		return "## Semantic Search\n\n⚠️  Semantic search is not configured. The engine must be initialized during boot.\n", nil
	}

	var p struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if p.TopK <= 0 {
		p.TopK = 10
	}
	if p.TopK > 30 {
		p.TopK = 30
	}

	// Check engine health
	if !semanticEngine.IsReady() {
		return fmt.Sprintf(
			"## Semantic Search\n\n⚠️  Index not yet built. Current size: %d chunks.\n\n"+
				"The index is built asynchronously after the first run. Try again in a few minutes,\n"+
				"or use `symbol-find` or `grep` for keyword search in the meantime.\n\n"+
				"%s",
			semanticEngine.IndexSize(),
			semantic.InstallInstructions(),
		), nil
	}

	results, err := semanticEngine.Search(ctx, p.Query, p.TopK)
	if err != nil {
		return "", fmt.Errorf("search: %w", err)
	}

	return semantic.FormatResults(p.Query, results), nil
}
