package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/permission"
)

// edit.go is the programmatic mutation surface a settings UI drives: change the
// default model, add/remove a provider, set the planner, edit permission rules,
// add/remove an MCP server — each validated, then persisted with SaveTo. It is
// separate from the `ok setup` wizard (cli) so a GUI can apply one setting at a
// time without replaying the whole interactive flow. Every mutator works on the
// in-memory *Config; nothing writes to disk until SaveTo/Save is called, so a UI
// can stage several changes and commit once. Mutations round-trip through
// RenderTOML → Load (the wizard relies on the same guarantee).

// permission rule list name accepted by the rule mutators.
// SetDefaultModel points default_model at an existing provider. It errors if no
// provider by that name is configured, so a UI can't strand the config on a
// model that doesn't exist.
func (c *Config) SetDefaultModel(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.providerLocked(name); !ok {
		return fmt.Errorf("set default: no provider %q (configured: %s)", name, c.providerNamesLocked())
	}
	c.DefaultModel = name
	return nil
}

// SetPlannerModel sets (or, with "", clears) agent.planner_model for two-model
// collaboration. A non-empty name must be a configured provider.
func (c *Config) SetPlannerModel(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if name == "" {
		c.Agent.PlannerModel = ""
		return nil
	}
	if _, ok := c.providerLocked(name); !ok {
		return fmt.Errorf("set planner: no provider %q (configured: %s)", name, c.providerNamesLocked())
	}
	c.Agent.PlannerModel = name
	return nil
}

// UpsertProvider adds e, or replaces an existing provider with the same name
// (preserving its position). Required fields (name, kind, base_url, model) are
// validated; whether the kind is actually registered and the key resolves is
// checked later by provider.New / Validate, which give actionable errors.
func (c *Config) UpsertProvider(e ProviderEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateProvider(e); err != nil {
		return err
	}
	for i := range c.Providers {
		if c.Providers[i].Name == e.Name {
			c.Providers[i] = e
			return nil
		}
	}
	c.Providers = append(c.Providers, e)
	return nil
}

// RemoveProvider deletes the named provider. It refuses to remove the current
// default_model (reassign it first, so the config never points at a missing
// model); if the removed provider was the planner, planner_model is cleared as
// a side effect since it is optional. Errors when the name isn't configured.
func (c *Config) RemoveProvider(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := -1
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("remove provider: no provider %q", name)
	}
	if c.DefaultModel == name {
		return fmt.Errorf("remove provider: %q is the default model — set a different default_model first", name)
	}
	c.Providers = append(c.Providers[:idx], c.Providers[idx+1:]...)
	if c.Agent.PlannerModel == name {
		c.Agent.PlannerModel = ""
	}
	return nil
}

// validateProvider checks the fields a provider can't function without.
func validateProvider(e ProviderEntry) error {
	switch {
	case strings.TrimSpace(e.Name) == "":
		return fmt.Errorf("provider: name is required")
	case strings.TrimSpace(e.Kind) == "":
		return fmt.Errorf("provider %q: kind is required", e.Name)
	case strings.TrimSpace(e.BaseURL) == "":
		return fmt.Errorf("provider %q: base_url is required", e.Name)
	case strings.TrimSpace(e.Model) == "" && len(e.Models) == 0:
		return fmt.Errorf("provider %q: model or models is required", e.Name)
	}
	return nil
}

// SetModeStyle sets the interaction style. Accepts "plan", "normal", or "yolo"
// (case-insensitive); anything else errors.
func (c *Config) SetModeStyle(style string) error {
	style = strings.ToLower(strings.TrimSpace(style))
	switch style {
	case "plan", "normal", "yolo":
		c.mu.Lock()
		c.Mode.Default = style
		c.mu.Unlock()
		return nil
	default:
		return fmt.Errorf("mode style %q: must be plan|normal|yolo", style)
	}
}

// AddPermissionRule appends a rule ("ToolName" or "ToolName(glob)") to the
// deny list (the only remaining rule list). The rule is validated with the
// same parser the gate uses, and a duplicate is a no-op so a UI can call it
// idempotently.
func (c *Config) AddPermissionRule(list, rule string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if list != "deny" && list != "allow" && list != "ask" {
		return fmt.Errorf("unknown permission list %q (want \"deny\", \"allow\", or \"ask\")", list)
	}
	rule = strings.TrimSpace(rule)
	if _, ok := permission.ParseRule(rule); !ok {
		return fmt.Errorf("invalid permission rule %q (want \"ToolName\" or \"ToolName(glob)\")", rule)
	}
	var target *[]string
	switch list {
	case "deny":
		target = &c.Mode.Deny
	case "allow":
		target = &c.Mode.Allow
	case "ask":
		target = &c.Mode.Ask
	default:
		return fmt.Errorf("unknown list %q: want deny, allow, or ask", list)
	}
	for _, existing := range *target {
		if existing == rule {
			return nil // already present
		}
	}
	*target = append(*target, rule)
	return nil
}

// RemovePermissionRule drops the first exact match of rule from deny list,
// reporting whether anything was removed.
func (c *Config) RemovePermissionRule(list, rule string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var target *[]string
	switch list {
	case "deny":
		target = &c.Mode.Deny
	case "allow":
		target = &c.Mode.Allow
	case "ask":
		target = &c.Mode.Ask
	default:
		return false, fmt.Errorf("unknown permission list %q (must be deny|allow|ask)", list)
	}
	rule = strings.TrimSpace(rule)
	for i, existing := range *target {
		if existing == rule {
			*target = append((*target)[:i], (*target)[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

// UpsertPlugin adds e, or replaces an MCP server with the same name (preserving
// position). The transport-specific required fields are validated: stdio needs
// a command, http/sse need a url.
func (c *Config) UpsertPlugin(e PluginEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validatePlugin(e); err != nil {
		return err
	}
	for i := range c.Plugins {
		if c.Plugins[i].Name == e.Name {
			c.Plugins[i] = e
			return nil
		}
	}
	c.Plugins = append(c.Plugins, e)
	return nil
}

// RemovePlugin deletes the named MCP server, reporting whether it was present.
func (c *Config) RemovePlugin(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.Plugins {
		if c.Plugins[i].Name == name {
			c.Plugins = append(c.Plugins[:i], c.Plugins[i+1:]...)
			return true
		}
	}
	return false
}

// validatePlugin checks a plugin entry by transport. An empty Type means stdio.
func validatePlugin(e PluginEntry) error {
	if strings.TrimSpace(e.Name) == "" {
		return fmt.Errorf("plugin: name is required")
	}
	switch strings.ToLower(strings.TrimSpace(e.Type)) {
	case "", "stdio":
		if strings.TrimSpace(e.Command) == "" {
			return fmt.Errorf("plugin %q: command is required for a stdio server", e.Name)
		}
	case "http", "sse", "streamable-http":
		if strings.TrimSpace(e.URL) == "" {
			return fmt.Errorf("plugin %q: url is required for a %s server", e.Name, e.Type)
		}
	default:
		return fmt.Errorf("plugin %q: unknown type %q (want stdio|http|sse)", e.Name, e.Type)
	}
	return nil
}

// SaveTo writes the configuration to path as annotated TOML, atomically: it
// writes a sibling temp file then renames, so a crash mid-write can't leave a
// half-written ok.toml that fails to parse on next load. Parent directories
// are created as needed.
func (c *Config) SaveTo(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("save: empty config path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("save: create dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".ok.*.toml.tmp")
	if err != nil {
		return fmt.Errorf("save: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(RenderTOML(c)); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("save: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("save: close temp: %w", err)
	}
	return os.Rename(tmpPath, path)
}

// Save writes the configuration back to the file it was loaded from
// (SourcePath), or to ./ok.toml when none exists yet — the conventional
// project-local target a fresh GUI session would create.
func (c *Config) Save() error {
	path := SourcePath()
	if path == "" {
		path = "ok.toml"
	}
	return c.SaveTo(path)
}
