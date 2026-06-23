package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// agentBench harness: a fake provider that replies immediately.
type benchProvider struct {
	chunks []provider.Chunk
}

func (b *benchProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, len(b.chunks))
	for _, c := range b.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func (b *benchProvider) Name() string { return "bench" }

// benchSink drops all events.
type benchSink struct{}

func (benchSink) Emit(*event.Event) {}

func BenchmarkStreamTextOnly(b *testing.B) {
	a := New(&benchProvider{
		chunks: []provider.Chunk{
			{Type: provider.ChunkText, Text: strings.Repeat("hello world ", 100)},
		},
	}, tool.NewRegistry(), NewSession("sys"), Options{}, benchSink{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.session = NewSession("sys")
		_, _, _, _, err := a.stream(context.Background())
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStreamWithToolCalls(b *testing.B) {
	args := json.RawMessage(`{"path":"x.go"}`)
	a := New(&benchProvider{
		chunks: []provider.Chunk{
			{Type: provider.ChunkToolCallStart, ToolCall: &provider.ToolCall{ID: "c1", Name: "read_file"}},
			{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "c1", Name: "read_file", Arguments: string(args)}},
			{Type: provider.ChunkText, Text: "done"},
		},
	}, tool.NewRegistry(), NewSession("sys"), Options{}, benchSink{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.session = NewSession("sys")
		_, _, calls, _, err := a.stream(context.Background())
		if err != nil {
			b.Fatal(err)
		}
		if len(calls) != 1 {
			b.Fatalf("expected 1 call, got %d", len(calls))
		}
	}
}

func BenchmarkStreamWithReasoning(b *testing.B) {
	a := New(&benchProvider{
		chunks: []provider.Chunk{
			{Type: provider.ChunkReasoning, Text: strings.Repeat("thinking deeply about this problem ", 200)},
			{Type: provider.ChunkText, Text: "answer"},
		},
	}, tool.NewRegistry(), NewSession("sys"), Options{}, benchSink{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.session = NewSession("sys")
		_, _, _, _, err := a.stream(context.Background())
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStreamManyChunks(b *testing.B) {
	var chunks []provider.Chunk
	for i := 0; i < 500; i++ {
		chunks = append(chunks, provider.Chunk{Type: provider.ChunkText, Text: "tok"})
	}
	a := New(&benchProvider{chunks: chunks}, tool.NewRegistry(), NewSession("sys"), Options{}, benchSink{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.session = NewSession("sys")
		_, _, _, _, err := a.stream(context.Background())
		if err != nil {
			b.Fatal(err)
		}
	}
}
