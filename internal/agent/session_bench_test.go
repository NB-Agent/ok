package agent

import (
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/provider"
)

// BenchmarkSessionAdd measures session message append cost.
func BenchmarkSessionAdd(b *testing.B) {
	s := NewSession("system prompt goes here")
	msg := provider.Message{
		Role:    provider.RoleUser,
		Content: strings.Repeat("hello world ", 10),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Add(msg)
	}
	if len(s.Snapshot()) < b.N {
		b.Fatal("unexpected snapshot length")
	}
}

// BenchmarkSessionSnapshot measures the cost of snapshotting large sessions.
func BenchmarkSessionSnapshot(b *testing.B) {
	s := NewSession("system prompt goes here")
	for i := 0; i < 100; i++ {
		s.Add(provider.Message{Role: provider.RoleUser, Content: "test"})
		s.Add(provider.Message{Role: provider.RoleAssistant, Content: "response"})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msgs := s.Snapshot()
		_ = len(msgs)
	}
}

// BenchmarkSessionReplace measures the cost of replacing entire session content.
func BenchmarkSessionReplace(b *testing.B) {
	s := NewSession("system prompt goes here")
	for i := 0; i < 50; i++ {
		s.Add(provider.Message{Role: provider.RoleUser, Content: "test"})
		s.Add(provider.Message{Role: provider.RoleAssistant, Content: "response"})
	}
	replacement := []provider.Message{
		{Role: provider.RoleSystem, Content: "new system"},
		{Role: provider.RoleUser, Content: "summary"},
		{Role: provider.RoleUser, Content: "latest"},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Replace(replacement)
	}
}
