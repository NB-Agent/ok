package config

import (
	"fmt"
	"os"
	"strings"
)

// --- Provider lookup / model resolution ---

// Provider returns the named provider entry.
func (c *Config) Provider(name string) (*ProviderEntry, bool) {
	return c.providerLocked(name)
}

// providerLocked looks up a provider — callers must hold c.mu (read or write).
func (c *Config) providerLocked(name string) (*ProviderEntry, bool) {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i], true
		}
	}
	return nil, false
}

// ResolveModel resolves a model reference to a provider entry whose Model is the
// selected model string (a copy, so the config's lists stay intact). It accepts:
//   - "provider/model" — that exact model under that provider;
//   - a provider name   — the provider's default model;
//   - a bare model name — the (first) provider that lists it.
//
// The returned entry is ready to build a provider from (NewProvider reads .Model),
// so a single "vendor with many models" entry yields one instance per model
// without duplicating base_url/api_key_env. Single-`model` entries still resolve
// by provider name, keeping older configs working unchanged.
func (c *Config) ResolveModel(ref string) (*ProviderEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if ref == "" {
		return nil, false
	}
	// "provider/model"
	if prov, model, ok := strings.Cut(ref, "/"); ok {
		if e, found := c.Provider(prov); found && e.HasModel(model) {
			cp := copyEntry(e)
			cp.Model = model
			return &cp, true
		}
	}
	// a provider name → its default model
	if e, found := c.Provider(ref); found {
		cp := copyEntry(e)
		cp.Model = e.DefaultModel()
		return &cp, true
	}
	// a bare model name → the provider that lists it
	for i := range c.Providers {
		if c.Providers[i].HasModel(ref) {
			cp := copyEntry(&c.Providers[i])
			cp.Model = ref
			return &cp, true
		}
	}
	return nil, false
}

// APIKey resolves the entry's API key from its api_key_env.
func (e *ProviderEntry) APIKey() string {
	if e.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(e.APIKeyEnv)
}

// ResolveSystemPrompt returns the system prompt, reading system_prompt_file if set,
// then appending any system_prompt_parts files in order. Resolution order:
//
//  1. system_prompt_file (when non-empty) — overrides system_prompt entirely.
//  2. system_prompt (when non-empty, and no system_prompt_file) — inline text.
//  3. DefaultSystemPrompt — fallback when both are empty.
//  4. system_prompt_parts files (optional) — appended after the base prompt,
//     each joined with a blank line, so parts can add tooling guidelines,
//     security rules, or language policies without editing the base.
func (c *Config) ResolveSystemPrompt() (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var base string
	if c.Agent.SystemPromptFile != "" {
		if strings.Contains(c.Agent.SystemPromptFile, "..") {
			return "", fmt.Errorf("system_prompt_file: path %q is not allowed", c.Agent.SystemPromptFile)
		}
		b, err := os.ReadFile(c.Agent.SystemPromptFile)
		if err != nil {
			return "", fmt.Errorf("system_prompt_file: %w", err)
		}
		// Cap at 1 MiB — a prompt file that large is a mistake, not intent.
		const maxPromptFile = 1 << 20
		if len(b) > maxPromptFile {
			return "", fmt.Errorf("system_prompt_file: %d bytes exceeds maximum %d bytes", len(b), maxPromptFile)
		}
		base = strings.TrimSpace(string(b))
	} else if strings.TrimSpace(c.Agent.SystemPrompt) == "" {
		base = DefaultSystemPrompt
	} else {
		base = c.Agent.SystemPrompt
	}

	for _, part := range c.Agent.SystemPromptParts {
		if strings.Contains(part, "..") {
			return "", fmt.Errorf("system_prompt_parts: path %q is not allowed", part)
		}
		b, err := os.ReadFile(part)
		if err != nil {
			return "", fmt.Errorf("system_prompt_parts: %s: %w", part, err)
		}
		trimmed := strings.TrimSpace(string(b))
		if trimmed != "" {
			base += "\n\n" + trimmed
		}
	}

	return base, nil
}

// Validate checks that the selected model's provider is usable.
func (c *Config) Validate(model string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.ResolveModel(model)
	if !ok {
		return fmt.Errorf("unknown model %q (configured: %s)", model, c.providerNamesLocked())
	}
	if e.Kind == "" {
		return fmt.Errorf("provider %q: kind is required", model)
	}
	if e.BaseURL == "" {
		return fmt.Errorf("provider %q: base_url is required", model)
	}
	if len(e.ModelList()) == 0 {
		return fmt.Errorf("provider %q: model (or models) is required", model)
	}
	if e.APIKey() == "" {
		return fmt.Errorf("provider %q: missing env %s", model, e.APIKeyEnv)
	}
	return nil
}

// SkillCustomPaths returns the configured custom skill roots with ${VAR}
// expanded; empty entries are dropped.
func (c *Config) SkillCustomPaths() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []string
	for _, p := range c.Skills.Paths {
		if p = ExpandVars(p); strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// providerNamesLocked returns a comma-separated list of provider names —
// callers must hold c.mu.
func (c *Config) providerNamesLocked() string {
	names := make([]string, len(c.Providers))
	for i, p := range c.Providers {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}

// copyEntry returns a deep copy of a ProviderEntry, including its Price pointer
// so callers that mutate the copy don't affect the config's original.
func copyEntry(e *ProviderEntry) ProviderEntry {
	cp := *e
	if e.Price != nil {
		p := *e.Price
		cp.Price = &p
	}
	return cp
}
