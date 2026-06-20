// Package config — provider auto-discovery from environment.
//
// AutoDetect scans the process environment for known API-key variables and
// appends corresponding [[providers]] entries so the user gets a working
// configuration with zero TOML editing — just set an API key and run.
//
// Naming rule: if an env var is set and no provider with the canonical name
// (e.g. "groq") exists yet, AutoDetect adds it. A user's ok.toml always wins.
package config

import (
	"os"
	"strings"
)

// providerProfile describes one well-known provider that can be auto-detected
// from an environment variable.
type providerProfile struct {
	Name          string   // canonical instance name, e.g. "groq"
	Kind          string   // "openai" | "anthropic"
	BaseURL       string   // API endpoint
	Model         string   // default model (single-model profile)
	Models        []string // alternative: multi-model list
	Default       string   // default when Models is set
	APIKeyEnv     string   // env var to read the API key from
	ContextWindow int      // default context window
}

// wellKnownProfiles lists every provider AutoDetect can recognise. Ordered
// roughly by popularity; the first match with its key set wins for a name.
var wellKnownProfiles = []providerProfile{
	// --- OpenAI (direct) ---
	{Name: "openai", Kind: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o", APIKeyEnv: "OPENAI_API_KEY", ContextWindow: 128_000},

	// --- Anthropic ---
	{Name: "claude", Kind: "anthropic", BaseURL: "https://api.anthropic.com", Models: []string{"claude-sonnet-4-20250514", "claude-haiku-3-5-20241022"}, Default: "claude-sonnet-4-20250514", APIKeyEnv: "ANTHROPIC_API_KEY", ContextWindow: 200_000},

	// --- Google Gemini (OpenAI-compatible endpoint) ---
	{Name: "gemini", Kind: "openai", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Models: []string{"gemini-2.5-flash", "gemini-2.5-pro"}, Default: "gemini-2.5-flash", APIKeyEnv: "GEMINI_API_KEY", ContextWindow: 1_048_576},

	// --- DeepSeek ---
	{Name: "deepseek", Kind: "openai", BaseURL: "https://api.deepseek.com", Models: []string{"deepseek-v4-flash", "deepseek-v4-pro"}, Default: "deepseek-v4-flash", APIKeyEnv: "DEEPSEEK_API_KEY", ContextWindow: 1_000_000},

	// --- Groq (fast inference) ---
	{Name: "groq", Kind: "openai", BaseURL: "https://api.groq.com/openai/v1", Model: "llama-3.3-70b-versatile", APIKeyEnv: "GROQ_API_KEY", ContextWindow: 131_072},

	// --- OpenRouter (one key, many models) ---
	{Name: "openrouter", Kind: "openai", BaseURL: "https://openrouter.ai/api/v1", Model: "openrouter/auto", APIKeyEnv: "OPENROUTER_API_KEY", ContextWindow: 200_000},

	// --- Together AI ---
	{Name: "together", Kind: "openai", BaseURL: "https://api.together.xyz/v1", Model: "meta-llama/Llama-3.3-70B-Instruct-Turbo", APIKeyEnv: "TOGETHER_API_KEY", ContextWindow: 131_072},

	// --- Perplexity ---
	{Name: "perplexity", Kind: "openai", BaseURL: "https://api.perplexity.ai", Model: "sonar-pro", APIKeyEnv: "PERPLEXITY_API_KEY", ContextWindow: 200_000},

	// --- MiMo (xiaomimimo) ---
	{Name: "mimo", Kind: "openai", BaseURL: "https://api.xiaomimimo.com/v1", Models: []string{"mimo-v2.5-pro", "mimo-v2-flash"}, Default: "mimo-v2.5-pro", APIKeyEnv: "MIMO_API_KEY", ContextWindow: 1_000_000},

	// --- Ollama (local) ---
	{Name: "ollama", Kind: "openai", BaseURL: "http://localhost:11434/v1", Model: "llama3.2", APIKeyEnv: "OLLAMA_API_KEY", ContextWindow: 32_768},

	// --- xAI (Grok) ---
	{Name: "grok", Kind: "openai", BaseURL: "https://api.x.ai/v1", Model: "grok-3", APIKeyEnv: "XAI_API_KEY", ContextWindow: 131_072},

	// --- Mistral ---
	{Name: "mistral", Kind: "openai", BaseURL: "https://api.mistral.ai/v1", Model: "mistral-large-2411", APIKeyEnv: "MISTRAL_API_KEY", ContextWindow: 128_000},

	// --- Cohere ---
	{Name: "cohere", Kind: "openai", BaseURL: "https://api.cohere.com/v2", Model: "command-r-plus", APIKeyEnv: "COHERE_API_KEY", ContextWindow: 128_000},

	// --- Fireworks AI ---
	{Name: "fireworks", Kind: "openai", BaseURL: "https://api.fireworks.ai/inference/v1", Model: "accounts/fireworks/models/llama-v3p3-70b-instruct", APIKeyEnv: "FIREWORKS_API_KEY", ContextWindow: 128_000},

	// --- AI/ML API (serverless GPU) ---
	{Name: "aiml", Kind: "openai", BaseURL: "https://api.aimlapi.com/v1", Model: "gpt-4o", APIKeyEnv: "AIML_API_KEY", ContextWindow: 128_000},

	// --- GitHub Models ---
	{Name: "github", Kind: "openai", BaseURL: "https://models.inference.ai.azure.com", Model: "gpt-4o", APIKeyEnv: "GITHUB_TOKEN", ContextWindow: 128_000},
}

// AutoDetectProviders scans the environment for known API-key variables and
// appends any that are set and whose canonical name is not already present in
// cfg.Providers. It returns the (possibly extended) list so Call can choose to
// append it to cfg.Providers or ignore it.
//
// A previously configured ok.toml always wins — AutoDetect only fills gaps.
func AutoDetectProviders(existing []ProviderEntry) []ProviderEntry {
	// Build a set of already-configured names (case-insensitive).
	have := make(map[string]bool, len(existing))
	for _, p := range existing {
		have[strings.ToLower(p.Name)] = true
	}

	var added []ProviderEntry
	for _, prof := range wellKnownProfiles {
		name := strings.ToLower(prof.Name)
		if have[name] {
			continue // user already configured this one
		}
		if os.Getenv(prof.APIKeyEnv) == "" {
			continue // no key → skip
		}
		entry := ProviderEntry{
			Name:          prof.Name,
			Kind:          prof.Kind,
			BaseURL:       prof.BaseURL,
			APIKeyEnv:     prof.APIKeyEnv,
			ContextWindow: prof.ContextWindow,
		}
		if len(prof.Models) > 0 {
			entry.Models = prof.Models
			entry.Default = prof.Default
			if entry.Default == "" {
				entry.Default = prof.Models[0]
			}
		} else {
			entry.Model = prof.Model
		}
		added = append(added, entry)
		have[name] = true
	}
	return added
}

// DetectDefaultModel returns the name of the first auto-detected provider that
// has its API key set. It checks profiles in order, so the default picks the
// most popular provider the user actually has credentials for.
//
// Used by the CLI to give a sensible default when no config file exists and no
// --model flag is passed: the user just sets an API key and runs.
func DetectDefaultModel() string {
	for _, prof := range wellKnownProfiles {
		if os.Getenv(prof.APIKeyEnv) != "" {
			def := prof.Default
			if def == "" {
				def = prof.Model
			}
			return prof.Name + "/" + def
		}
	}
	return ""
}

// AutoDetectDefaultModel replaces cfg.DefaultModel with the first auto-detected
// provider that has its API key set, but only when the current default has no
// key configured. This lets a user who never wrote ok.toml just set an API key
// and run — OK picks the right model automatically.
func AutoDetectDefaultModel(cfg *Config) {
	if cfg == nil {
		return
	}
	// If the current default already has a usable key, keep it.
	if e, ok := cfg.ResolveModel(cfg.DefaultModel); ok && e.APIKey() != "" {
		return
	}
	for _, prof := range wellKnownProfiles {
		if os.Getenv(prof.APIKeyEnv) == "" {
			continue
		}
		def := prof.Default
		if def == "" {
			def = prof.Model
		}
		cfg.DefaultModel = prof.Name + "/" + def
		return
	}
}
