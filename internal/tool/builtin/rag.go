package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/semantic"
	"github.com/NB-Agent/ok/internal/tool"
)

type ragTool struct{}

func init() { tool.RegisterBuiltin(ragTool{}) }

func (ragTool) Name() string { return "rag" }

func (ragTool) Description() string {
	return "Semantic memory search across past sessions and decisions. Search saved facts, store new knowledge, or review the memory index."
}

func (ragTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"action":{"enum":["search","save","index","stats"],"type":"string"},"query":{"type":"string"},"scope":{"type":"string"},"top_k":{"type":"integer"}},"required":["action"],"type":"object"}`)
}

func (ragTool) ReadOnly() bool { return true }

func (ragTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action string `json:"action"`
		Query  string `json:"query"`
		TopK   int    `json:"top_k"`
		Scope  string `json:"scope"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.TopK <= 0 || p.TopK > 20 {
		p.TopK = 5
	}

	switch p.Action {
	case "search":
		if p.Query == "" {
			return "", fmt.Errorf("query is required")
		}
		if semanticEngine != nil && semanticEngine.IsReady() {
			if results, err := semanticEngine.Search(ctx, p.Query, p.TopK); err == nil {
				return formatRAGResults(p.Query, results), nil
			}
		}
		return keywordSearchMemories(p.Query, p.TopK)

	case "save":
		if p.Query == "" {
			return "", fmt.Errorf("fact/description is required")
		}
		ragDir := filepath.Join(".ok", "memory", "rag")
		if err := os.MkdirAll(ragDir, 0o755); err != nil {
			return "", fmt.Errorf("create rag dir: %w", err)
		}
		name := strings.ReplaceAll(strings.ToLower(p.Query), " ", "-")
		name = strings.Trim(name, "-")
		if len(name) > 60 {
			name = name[:60]
		}
		name = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				return r
			}
			return '-'
		}, name)
		path := filepath.Join(ragDir, name+".md")
		if _, err := os.Stat(path); err == nil {
			for i := 2; i < 100; i++ {
				altPath := filepath.Join(ragDir, fmt.Sprintf("%s-%d.md", name, i))
				if _, err := os.Stat(altPath); os.IsNotExist(err) {
					path = altPath
					break
				}
			}
		}
		content := fmt.Sprintf("# %s\n\n%s\n\n---\n*Saved via rag tool on request*\n", p.Query, p.Query)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("save rag fact: %w", err)
		}
		if semanticEngine != nil {
			if err := semanticEngine.RebuildMemoryIndex(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "rag: RebuildMemoryIndex error: %v\n", err)
			}
		}
		return fmt.Sprintf("# RAG Save\n\n✅ Saved to `%s`\n", path), nil

	case "index":
		memDir := filepath.Join(".ok", "memory")
		if err := os.MkdirAll(memDir, 0o755); err != nil {
			return "", fmt.Errorf("create memory dir: %w", err)
		}
		count := 0
		filepath.Walk(memDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.HasSuffix(info.Name(), ".md") {
				count++
			}
			return nil
		})
		return fmt.Sprintf("# RAG Index\n\n✅ Indexed %d memory files in `%s`\n", count, memDir), nil

	case "stats":
		memDir := filepath.Join(".ok", "memory")
		count := 0
		var totalSize int64
		filepath.Walk(memDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.HasSuffix(info.Name(), ".md") {
				count++
				totalSize += info.Size()
			}
			return nil
		})
		var b strings.Builder
		b.WriteString("# RAG Stats\n\n")
		b.WriteString(fmt.Sprintf("Memory files: %d\n", count))
		b.WriteString(fmt.Sprintf("Total size: %d bytes\n", totalSize))
		b.WriteString(fmt.Sprintf("Directory: `%s`\n", memDir))
		return b.String(), nil

	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}

func formatRAGResults(query string, results []semantic.SearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("# RAG Search: %q\n\nNo matching memory found.\n\n💡 Use `rag save` to store important facts, decisions, and patterns.\n", query)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# RAG Search: %q\n\n", query))
	b.WriteString(fmt.Sprintf("Found %d result(s):\n\n", len(results)))
	for i, r := range results {
		b.WriteString(fmt.Sprintf("**%d.** `%s` — %.2f (%s)\n", i+1, r.Chunk.ID, r.Score, r.MatchType))
		if r.Chunk.Doc != "" {
			b.WriteString(fmt.Sprintf("> %s\n", r.Chunk.Doc))
		}
		content := strings.TrimSpace(r.Chunk.Content)
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		b.WriteString(fmt.Sprintf("```\n%s\n```\n\n", content))
	}
	return b.String()
}

func keywordSearchMemories(query string, topK int) (string, error) {
	memDir := filepath.Join(".ok", "memory")
	entries, err := os.ReadDir(memDir)
	if err != nil {
		return "# RAG Search\n\nNo memory index found. Start saving facts with 'rag save' to build your memory.\n", nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# RAG Search: `%s`\n\n", query))
	queryLower := strings.ToLower(query)
	found := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(memDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := strings.ToLower(string(data))
		if strings.Contains(content, queryLower) {
			if found >= topK {
				break
			}
			lines := strings.Split(string(data), "\n")
			snippet := ""
			for _, line := range lines {
				if strings.Contains(strings.ToLower(line), queryLower) {
					snippet = strings.TrimSpace(line)
					if len(snippet) > 120 {
						snippet = snippet[:120] + "..."
					}
					break
				}
			}
			if snippet == "" && len(lines) > 0 {
				snippet = strings.TrimSpace(lines[0])
				if len(snippet) > 120 {
					snippet = snippet[:120] + "..."
				}
			}
			b.WriteString(fmt.Sprintf("- **%s**", strings.TrimSuffix(e.Name(), ".md")))
			if snippet != "" {
				b.WriteString(fmt.Sprintf(": %s", snippet))
			}
			b.WriteString("\n")
			found++
		}
	}
	if found == 0 {
		b.WriteString("No matching memory found.\n")
		b.WriteString("\n💡 Use `rag save` to store important facts, decisions, and patterns.\n")
	}
	return b.String(), nil
}
