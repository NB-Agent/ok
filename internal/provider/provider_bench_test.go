package provider

import (
	"context"
	"encoding/json"
	"testing"
)

// BenchmarkMessageJSON ensures the message struct doesn't regress in serialization.
func BenchmarkMessageJSON(b *testing.B) {
	msg := Message{
		Role:    RoleUser,
		Content: "This is a message with about fifty tokens of content for the benchmark to serialize",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, err := json.Marshal(msg)
		if err != nil {
			b.Fatal(err)
		}
		_ = data
	}
}

// BenchmarkRequestSerialization measures full ChatRequest JSON encode cost.
func BenchmarkRequestSerialization(b *testing.B) {
	req := Request{
		Messages: []Message{
			{Role: RoleSystem, Content: "You are a helpful coding agent."},
			{Role: RoleUser, Content: "Write a function to sort a slice."},
			{Role: RoleAssistant, Content: "Here is the code..."},
			{Role: RoleUser, Content: "Now make it generic."},
		},
		Temperature: 0.7,
		MaxTokens:   4096,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(req)
	}
}

// BenchmarkChunkAllocation measures chunk channel overhead.
func BenchmarkChunkAllocation(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch := make(chan Chunk, 1)
		ch <- Chunk{Type: ChunkText, Text: "test"}
		close(ch)
		<-ch
	}
}

// dummy for ctx
var _ = context.Background
