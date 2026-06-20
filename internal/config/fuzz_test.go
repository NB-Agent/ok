package config

import (
	"strings"
	"testing"
)

// FuzzResolveModel exercises the model/ref resolver with arbitrary provider
// names and model references to verify it never panics.
func FuzzResolveModel(f *testing.F) {
	cfg := &Config{
		DefaultModel: "deepseek-flash",
		Providers: []ProviderEntry{
			{Name: "deepseek-flash", Kind: "openai", Model: "deepseek-v4-flash"},
			{Name: "deepseek-pro", Kind: "openai", Model: "deepseek-v4-pro"},
			{Name: "mimo-pro", Kind: "openai", Models: []string{"mimo-v2.5-pro", "mimo-v2-flash"}, Default: "mimo-v2.5-pro"},
		},
	}

	f.Add("")
	f.Add("deepseek-flash")
	f.Add("deepseek-pro/model")
	f.Add("mimo-v2.5-pro")
	f.Add("nonexistent")
	f.Add("/")
	f.Add("///")
	f.Add(strings.Repeat("x", 1000))

	f.Fuzz(func(t *testing.T, ref string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on ResolveModel(%q): %v", ref, r)
			}
		}()
		entry, _ := cfg.ResolveModel(ref)
		if entry != nil {
			_ = entry.APIKey()
			_ = entry.ModelList()
			_ = entry.DefaultModel()
			_ = entry.HasModel("")
		}
	})
}

// FuzzExpandVars verifies variable expansion never panics.
func FuzzExpandVars(f *testing.F) {
	f.Add("")
	f.Add("${HOME}")
	f.Add("${NONEXISTENT:-default}")
	f.Add("${NESTED:-${HOME}}")
	f.Add(strings.Repeat("${X}", 100))

	f.Fuzz(func(t *testing.T, s string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on ExpandVars(%q): %v", s, r)
			}
		}()
		_ = ExpandVars(s)
	})
}

// FuzzMergeMCPJSON exercises MCP JSON merging with random server entries.
func FuzzMergeMCPJSON(f *testing.F) {
	cfg := Default()

	f.Add(`[{"name":"x","command":"echo"}]`)
	f.Add(`[]`)
	f.Add(`not json`)

	f.Fuzz(func(t *testing.T, raw string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on mergeMCPJSON(%q): %v", raw, r)
			}
		}()
		// MergeMCPJSON expects parsed entries. Parse best-effort; purpose
		// is to verify merge never panics even with unusual entries.
		entry := PluginEntry{Name: raw}
		cfg.mergeMCPJSON([]PluginEntry{entry})
	})
}
