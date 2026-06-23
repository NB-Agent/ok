package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// benchCompacter is a fake provider that returns a fixed summary.
type benchCompacter struct {
	chunk provider.Chunk
}

func (b *benchCompacter) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 1)
	ch <- b.chunk
	close(ch)
	return ch, nil
}

func (b *benchCompacter) Name() string { return "bench-compact" }

func makeCompactSession(n int) *Session {
	s := NewSession("system")
	for i := 0; i < n; i++ {
		s.Add(provider.Message{
			Role:    provider.RoleUser,
			Content: strings.Repeat("some question about the codebase ", 10),
		})
		s.Add(provider.Message{
			Role:    provider.RoleAssistant,
			Content: strings.Repeat("here is the answer with many details about the implementation ", 15),
		})
	}
	return s
}

func BenchmarkCompact100Messages(b *testing.B) {
	prov := &benchCompacter{
		chunk: provider.Chunk{Type: provider.ChunkText, Text: "summarized: the user and assistant discussed code changes."},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := New(prov, tool.NewRegistry(), makeCompactSession(50),
			Options{ContextWindow: 32000, CompactRatio: 0.8, RecentKeep: 8},
			benchSink{})
		err := a.compact(context.Background())
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompactNoopBelowThreshold(b *testing.B) {
	prov := &benchCompacter{
		chunk: provider.Chunk{Type: provider.ChunkText, Text: "summarized."},
	}
	s := makeCompactSession(2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := New(prov, tool.NewRegistry(), s,
			Options{ContextWindow: 1_000_000, CompactRatio: 0.8, RecentKeep: 8},
			benchSink{})
		err := a.compact(context.Background())
		if err != nil {
			b.Fatal(err)
		}
	}
}
