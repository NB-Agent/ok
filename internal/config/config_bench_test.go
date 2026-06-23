package config

import (
	"testing"
)

// BenchmarkDefaultConfig measures the cost of building a fresh default config.
func BenchmarkDefaultConfig(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cfg := Default()
		if cfg == nil {
			b.Fatal("nil config")
		}
	}
}

// BenchmarkDefaultProviders constructs a default config and resolves models.
func BenchmarkDefaultProviders(b *testing.B) {
	cfg := Default()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Resolve every provider's models
		for _, e := range cfg.Providers {
			_ = e.APIKey()
			_ = e.ModelList()
			_ = e.DefaultModel()
		}
	}
}

// BenchmarkExpandVars measures environment variable expansion cost.
func BenchmarkExpandVars(b *testing.B) {
	s := "Hello ${USER}, your path is ${PATH}"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ExpandVars(s)
	}
}
