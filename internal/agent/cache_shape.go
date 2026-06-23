package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
)

// PrefixShape hashes the portions of the request prefix that influence
// provider-side prompt-cache reuse. Comparing snapshots across turns
// lets us explain *why* a cache miss happened.
type PrefixShape struct {
	SystemHash        string
	ToolsHash         string
	PrefixHash        string
	LogRewriteVersion uint64
	ToolSchemaTokens  int
}

func shortHash(v interface{}) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h[:8])
}

// CaptureShape takes a snapshot of the current prefix state.
func CaptureShape(systemPrompt string, schemas []provider.ToolSchema, rewriteVersion uint64) PrefixShape {
	normalizedSchemas := normalizeToolSchemas(schemas)
	toolsJSON, _ := json.Marshal(normalizedSchemas)
	return PrefixShape{
		SystemHash: shortHash(systemPrompt),
		ToolsHash:  shortHash(string(toolsJSON)),
		PrefixHash: shortHash(map[string]interface{}{
			"system": systemPrompt,
			"tools":  string(toolsJSON),
		}),
		LogRewriteVersion: rewriteVersion,
		ToolSchemaTokens:  estimateTokens(string(toolsJSON)),
	}
}

func normalizeToolSchemas(schemas []provider.ToolSchema) []provider.ToolSchema {
	out := make([]provider.ToolSchema, len(schemas))
	copy(out, schemas)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].Description != out[j].Description {
			return out[i].Description < out[j].Description
		}
		return string(out[i].Parameters) < string(out[j].Parameters)
	})
	return out
}

// CompareShape returns diagnostics describing what changed between two shapes.
func CompareShape(prev, cur PrefixShape, usage *provider.Usage) event.CacheDiagnostics {
	reasons := []string{}
	if prev.SystemHash != "" && prev.SystemHash != cur.SystemHash {
		reasons = append(reasons, "system")
	}
	if prev.ToolsHash != "" && prev.ToolsHash != cur.ToolsHash {
		reasons = append(reasons, "tools")
	}
	if prev.LogRewriteVersion != cur.LogRewriteVersion {
		reasons = append(reasons, "log_rewrite")
	}
	var miss, hit int
	if usage != nil {
		miss = usage.CacheMissTokens
		hit = usage.CacheHitTokens
	}
	return event.CacheDiagnostics{
		PrefixHash:          cur.PrefixHash,
		PrefixChanged:       len(reasons) > 0,
		PrefixChangeReasons: reasons,
		SystemHash:          cur.SystemHash,
		ToolsHash:           cur.ToolsHash,
		LogRewriteVersion:   cur.LogRewriteVersion,
		ToolSchemaTokens:    cur.ToolSchemaTokens,
		CacheMissTokens:     miss,
		CacheHitTokens:      hit,
	}
}

// estimateTokens gives a rough token count from byte length.
func estimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	return len(s) / 4
}
