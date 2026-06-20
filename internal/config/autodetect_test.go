package config

import (
	"os"
	"testing"
)

func TestAutoDetectProviders(t *testing.T) {
	// Save and restore env vars.
	saved := map[string]string{}
	for _, k := range []string{"OPENAI_API_KEY", "GROQ_API_KEY", "ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY"} {
		saved[k] = os.Getenv(k)
		os.Unsetenv(k)
	}
	defer func() {
		for k, v := range saved {
			if v != "" {
				os.Setenv(k, v)
			}
		}
	}()

	// Start with a config that already has a "deepseek" provider.
	existing := []ProviderEntry{
		{Name: "deepseek", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash", APIKeyEnv: "DEEPSEEK_API_KEY"},
	}

	// No extra keys set → nothing added.
	added := AutoDetectProviders(existing)
	if len(added) != 0 {
		t.Errorf("expected 0 auto-detected providers with no env keys, got %d", len(added))
	}

	// Set GROQ_API_KEY → groq should be added.
	os.Setenv("GROQ_API_KEY", "sk-test")
	added = AutoDetectProviders(existing)
	if len(added) != 1 {
		t.Fatalf("expected 1 auto-detected provider, got %d", len(added))
	}
	if added[0].Name != "groq" {
		t.Errorf("expected 'groq', got %q", added[0].Name)
	}

	// deepseek is already configured → still only groq added (not deepseek).
	os.Setenv("OPENAI_API_KEY", "sk-openai")
	added = AutoDetectProviders(existing)
	if len(added) != 2 {
		t.Fatalf("expected 2 auto-detected providers (groq + openai), got %d", len(added))
	}

	found := false
	for _, p := range added {
		if p.Name == "openai" {
			found = true
			if p.APIKeyEnv != "OPENAI_API_KEY" {
				t.Errorf("openai api_key_env = %q, want OPENAI_API_KEY", p.APIKeyEnv)
			}
		}
	}
	if !found {
		t.Error("openai not found in auto-detected providers")
	}
}

func TestAutoDetectDefaultModel(t *testing.T) {
	// Save ALL known API key env vars and clear them.
	allKeys := []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY", "GEMINI_API_KEY",
		"GROQ_API_KEY", "MIMO_API_KEY", "PERPLEXITY_API_KEY", "TOGETHER_API_KEY",
		"OPENROUTER_API_KEY", "XAI_API_KEY", "MISTRAL_API_KEY", "COHERE_API_KEY",
		"FIREWORKS_API_KEY", "AIML_API_KEY", "GITHUB_TOKEN", "OLLAMA_API_KEY"}
	saved := map[string]string{}
	for _, k := range allKeys {
		saved[k] = os.Getenv(k)
		os.Unsetenv(k)
	}
	defer func() {
		for k, v := range saved {
			if v != "" {
				os.Setenv(k, v)
			}
		}
	}()

	cfg := Default()

	// No API keys set → default stays "deepseek-flash"
	AutoDetectDefaultModel(cfg)
	if cfg.DefaultModel != "deepseek-flash" {
		t.Errorf("with no keys, default should stay 'deepseek-flash', got %q", cfg.DefaultModel)
	}

	// Set OPENAI_API_KEY → default should switch to openai/gpt-4o
	os.Setenv("OPENAI_API_KEY", "sk-test")
	cfg = Default()
	AutoDetectDefaultModel(cfg)
	if cfg.DefaultModel != "openai/gpt-4o" {
		t.Errorf("with OPENAI_API_KEY set, default should be 'openai/gpt-4o', got %q", cfg.DefaultModel)
	}
}
