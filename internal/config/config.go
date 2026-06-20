// Package config loads OK's runtime configuration from TOML. Resolution order:
// flag > project ./ok.toml > user ~/.config/ok/config.toml > built-in defaults.
// Secrets come from the environment via api_key_env and are never stored in
// config files.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/BurntSushi/toml"

	"github.com/NB-Agent/ok/internal/provider"
)

// ConfigFileName is the project-level configuration file.
const ConfigFileName = "ok.toml"

// DotEnvFileName is the project-level environment-variable file.
const DotEnvFileName = ".env"

// Config is OK's runtime configuration.
type Config struct {
	mu sync.RWMutex `json:"-"`

	DefaultModel string            `toml:"default_model"`
	Language     string            `toml:"language"` // ui language tag (e.g. "zh"); empty = auto-detect from $LANG / $OK_LANG
	Agent        AgentConfig       `toml:"agent"`
	Providers    []ProviderEntry   `toml:"providers"`
	Tools        ToolsConfig       `toml:"tools"`
	Permissions  PermissionsConfig `toml:"permissions"`
	Mode         ModeConfig        `toml:"mode"`
	Sandbox      SandboxConfig     `toml:"sandbox"`
	Plugins      []PluginEntry     `toml:"plugins"`
	Skills       SkillsConfig      `toml:"skills"`
	Codegraph    CodegraphConfig   `toml:"codegraph"`
	Team         TeamConfig        `toml:"team"`
	Reasoner     ReasonerConfig    `toml:"reasoner"`
	Router       RouterConfig      `toml:"router"`
	ECP          ECPConfig         `toml:"ecp"`

	// PluginQuiet suppresses the "auto-discovered N plugin(s)" startup notice.
	PluginQuiet bool `toml:"plugin_quiet,omitempty"`
}

// TeamConfig declares an agent team — an orchestrator model + specialist agents.
type TeamConfig struct {
	Orchestrator string             `toml:"orchestrator"`
	Specialists  []SpecialistConfig `toml:"specialists"`
}

// SpecialistConfig declares one specialist in a team.
type SpecialistConfig struct {
	Name        string   `toml:"name"`
	Model       string   `toml:"model"`
	Description string   `toml:"description"`
	Prompt      string   `toml:"prompt"`
	Tools       []string `toml:"tools"`
}

// ReasonerConfig configures the top-level multi-agent Reasoner.
// DecomposeModel, when set, enables LLM-based DAG decomposition.
// MaxConcurrent caps parallel task execution (default 3).
type ReasonerConfig struct {
	DecomposeModel string `toml:"decompose_model"`
	MaxConcurrent  int    `toml:"max_concurrent"`
}

// RouterConfig configures model routing by task complexity.
// When Enabled, CheapModel handles simple tasks and ExpensiveModel handles
// complex ones. DefaultModel is used for normal-complexity tasks.
type RouterConfig struct {
	Enabled        bool   `toml:"enabled"`
	CheapModel     string `toml:"cheap_model"`
	ExpensiveModel string `toml:"expensive_model"`
}

// ECPConfig configures cross-instance knowledge federation via the Evolution
// Control Protocol. When Enabled, the agent shares learned skills with peers
// and accepts skills from them — turning single-device evolution into a
// federated learning network.
type ECPConfig struct {
	Enabled      bool     `toml:"enabled"`
	SharedSecret string   `toml:"shared_secret"`
	Peers        []string `toml:"peers"`
	SyncInterval string   `toml:"sync_interval"` // "1h", "30m", etc.
}

// CodegraphConfig governs the built-in CodeGraph MCP server — symbol/call-graph
// code intelligence (tree-sitter + SQLite) that gives the agent codegraph_*
// search / context / explore / trace / node tools. Enabled defaults to true; set
// enabled = false to drop those tools and fall back to grep/glob. Path overrides
// binary resolution; empty means the bundle shipped next to the ok
// executable, then a `codegraph` on PATH.
type CodegraphConfig struct {
	Enabled bool   `toml:"enabled"`
	Path    string `toml:"path"`
}

// SkillsConfig configures skill discovery. Paths adds extra "custom"-scope skill
// roots — each a directory of SKILL.md / <name>.md playbooks — scanned between
// the project roots (.ok/.agents/.claude under the workspace) and the
// global roots (the same three under the home dir). ~ and relative paths and
// ${VAR} expansion are supported.
type SkillsConfig struct {
	Paths []string `toml:"paths"`
}

// AgentConfig configures the harness loop. PlannerModel is optional: when set
// to another provider's name it enables two-model collaboration, where the
// planner handles low-frequency planning in its own session (kept separate so
// each model's prompt prefix stays cache-stable).
type AgentConfig struct {
	SystemPrompt       string   `toml:"system_prompt"`
	SystemPromptFile   string   `toml:"system_prompt_file"`
	SystemPromptParts  []string `toml:"system_prompt_parts"` // optional: extra prompt files appended in order
	MaxSteps           int      `toml:"max_steps"`
	Temperature        float64  `toml:"temperature"`
	PlannerModel       string   `toml:"planner_model"`
	MaxConcurrentTasks int      `toml:"max_concurrent_tasks"`
	CompileCmd         string   `toml:"compile_cmd"`
	TestCmd            string   `toml:"test_cmd"`
}

// ProviderEntry declares a model provider instance. ContextWindow is the model's
// token budget; the harness compacts older history as a turn's prompt approaches
// it (see agent compaction). 0 disables compaction for the instance.
type ProviderEntry struct {
	Name          string            `toml:"name"`
	Kind          string            `toml:"kind"`
	BaseURL       string            `toml:"base_url"`
	Model         string            `toml:"model"`   // a single model (back-compat)
	Models        []string          `toml:"models"`  // a vendor's model list (one base_url/key, many models)
	Default       string            `toml:"default"` // default model when Models is set (else Models[0])
	APIKeyEnv     string            `toml:"api_key_env"`
	BalanceURL    string            `toml:"balance_url"` // optional; a provider-specific wallet-balance endpoint (DeepSeek: https://api.deepseek.com/user/balance). Empty = no balance readout.
	ContextWindow int               `toml:"context_window"`
	Price         *provider.Pricing `toml:"price"`
}

// ModelList returns the models this provider exposes: the explicit `models` list,
// or the single `model` as a one-element list (back-compat). Empty if neither set.
func (e *ProviderEntry) ModelList() []string {
	if len(e.Models) > 0 {
		return e.Models
	}
	if e.Model != "" {
		return []string{e.Model}
	}
	return nil
}

// DefaultModel returns the provider's default model: the explicit `default`, else
// the first of ModelList.
func (e *ProviderEntry) DefaultModel() string {
	if e.Default != "" {
		return e.Default
	}
	if l := e.ModelList(); len(l) > 0 {
		return l[0]
	}
	return ""
}

// HasModel reports whether m is one of the provider's models.
func (e *ProviderEntry) HasModel(m string) bool {
	for _, x := range e.ModelList() {
		if x == m {
			return true
		}
	}
	return false
}

// ToolsConfig selects which built-in tools are enabled. Empty means all of them.
type ToolsConfig struct {
	Enabled []string `toml:"enabled"`
}

// PermissionsConfig declares the per-call permission policy (see
// internal/permission). Mode is the fallback decision for writer tools when no
// rule matches ("ask" | "allow" | "deny"; default "ask"); read-only tools always
// fall back to allow. Allow/Ask/Deny are rule lists of the form "ToolName" or
// "ToolName(glob)". Precedence: deny > ask > allow > fallback.
//
// Deprecated: Use ModeConfig instead. The two are mutually exclusive;
// if both are present, ModeConfig takes precedence.
type PermissionsConfig struct {
	Mode  string   `toml:"mode"`
	Allow []string `toml:"allow"`
	Ask   []string `toml:"ask"`
	Deny  []string `toml:"deny"`
}

// ModeConfig replaces the old [permissions] section. The Default style selects
// "plan", "normal", or "yolo". Allow/Ask/Deny are "ToolName" or
// "ToolName(glob)" rule lists with precedence: deny > ask > allow.
type ModeConfig struct {
	Default string   `toml:"default"`
	Allow   []string `toml:"allow"`
	Ask     []string `toml:"ask"`
	Deny    []string `toml:"deny"`
}

// PluginEntry declares an external MCP server. Type selects the transport:
// "stdio" (default) launches Command/Args/Env as a subprocess; "http"
// (a.k.a. streamable-http) and "sse" connect to a remote URL with optional
// static Headers. String fields support ${VAR} / ${VAR:-default} expansion so
// secrets (bearer tokens, keys) come from the environment, not the file. The
// fields mirror Claude Code's mcpServers spec, so entries can come from either
// ok.toml's [[plugins]] or a project-root .mcp.json (see loadMCPJSON).
type PluginEntry struct {
	Name    string            `toml:"name"`
	Type    string            `toml:"type"` // "stdio" (default) | "http" | "sse"
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	URL     string            `toml:"url"`
	Headers map[string]string `toml:"headers"`
}

// DefaultSystemPrompt is used when config provides none.
const DefaultSystemPrompt = `You are OK, a coding agent. Your working method:

0. Perceive the gap — state current → target → gap. Never skip this.
1. Decompose — break the gap into yes/no leaf propositions. Each leaf is one atomic action (read, grep, bash). Spawn independent leaf sub-agents in parallel via task(). Spawn only when all free slots suffice.
2. Verify (post-order) — leaf = YES/NO with concrete file:line evidence. Parent = AND of its children. On NO backtrack to the root-cause leaf, re-verify, recompute upward. Never guess.
3. Resolve — root = YES only when all leaves accounted for.

Execution model: P→C→E→V loop (plan → call → execute → verify). Analysis side (task/read/verify) spawns read-only sub-agents. Execution side (writes/bash/changes) runs directly.

Constraints
- Keep changes minimal. Call ask(2-4 options) when the user must choose; skip when obvious.
- Track multi-step work with todo_write: list steps, one in_progress at a time, mark completed.
- Plan mode: read-only research → plan → stop → wait for approval. After approval, execute.
- After each turn, briefly summarize what you did.

Proactive habits
- Before writing code in a new package, call run_skill({name:"scan-style"}) to match conventions.
- Before a multi-file edit, check impact: run arch-review or grep imports first.
- When writing .go files: precheckGoFile runs go vet on a temp copy before writing. If you get "go vet error", fix and retry — nothing was written, no rollback.
- Before concluding a multi-file edit, run 'go build ./...' to confirm the project compiles.
- When auto-fix or a task involves a design decision, call run_skill({name:"save-experience"}).
- When the user corrects you twice on the same pattern, call remember(type:"feedback").
- When you claim something does NOT exist, say which searches you ran — a negative claim is only as trustworthy as its search.
- When asked to "deep audit" / "find all bugs" / "deep review" — use the ok-verify tool (16 static analyzers, 100% file coverage, <1s, zero tokens) instead of spawning task() sub-agents for sampling. After ok-verify finds issues, use task() with write tools to fix them.

Tool groups — start with core (files/search/task/bash). Activate advanced+knowledge via tool-groups when needed. Activating only core saves ~70% schema tokens.

--- Base instructions end; dynamic context (memory, skills, env) follows ---`

// LanguagePolicy is appended to every system prompt (in boot assembly) so the
// model mirrors the user's language per message instead of the harness pinning
// one — the UI `language` setting governs only the interface, never the model.
// It is static English text, so it stays part of the cache-stable prefix and
// keeps model behavior language-stable while still adapting the reply language.
const LanguagePolicy = `Mirror the user's language in every reply; switch when they switch. Think ` +
	`in that language. Never translate code, identifiers, file paths, shell commands, ` +
	`or technical terms — keep them in their original form.`

// ModeStyle returns the effective mode style ("normal" | "plan" | "yolo").
// [mode] takes precedence; falling back to the old [permissions] mode for
// backward compatibility.
// modeStyle returns the effective mode style without locking.
// Caller must hold c.mu.RLock or c.mu.Lock.
func (c *Config) modeStyle() string {
	if c.Mode.Default != "" {
		return c.Mode.Default
	}
	// Map old permissions mode to new style.
	switch c.Permissions.Mode {
	case "deny", "allow":
		return "yolo" // old "allow" or "deny" means no prompting → YOLO
	case "ask":
		return "normal" // old "ask" matches "normal"
	}
	return "normal"
}

// ModeStyle returns the effective mode style ("normal" | "plan" | "yolo").
// [mode] takes precedence; falling back to the old [permissions] mode for
// backward compatibility.
func (c *Config) ModeStyle() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.modeStyle()
}

// ModeDeny returns the effective deny rule list. [mode] takes precedence;
// falling back to [permissions] deny for backward compatibility.
// modeDeny returns the effective deny rule list without locking.
// Caller must hold c.mu.RLock or c.mu.Lock.
func (c *Config) modeDeny() []string {
	if len(c.Mode.Deny) > 0 {
		return c.Mode.Deny
	}
	return c.Permissions.Deny
}

// ModeDeny returns the effective deny rule list. [mode] takes precedence;
// falling back to [permissions] deny for backward compatibility.
func (c *Config) ModeDeny() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.modeDeny()
}

// ModeAllow returns the effective allow rule list.
// modeAllow returns the effective allow rule list without locking.
// Caller must hold c.mu.RLock or c.mu.Lock.
func (c *Config) modeAllow() []string {
	if len(c.Mode.Allow) > 0 {
		return c.Mode.Allow
	}
	return c.Permissions.Allow
}

// ModeAllow returns the effective allow rule list.
func (c *Config) ModeAllow() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.modeAllow()
}

// ModeAsk returns the effective ask rule list.
// modeAsk returns the effective ask rule list without locking.
// Caller must hold c.mu.RLock or c.mu.Lock.
func (c *Config) modeAsk() []string {
	if len(c.Mode.Ask) > 0 {
		return c.Mode.Ask
	}
	return c.Permissions.Ask
}

// ModeAsk returns the effective ask rule list.
func (c *Config) ModeAsk() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.modeAsk()
}

// Default returns the built-in default configuration (DeepSeek + MiMo presets).
func Default() *Config {
	return &Config{
		DefaultModel: "deepseek-flash",
		Agent: AgentConfig{
			SystemPrompt: DefaultSystemPrompt,
			// 0 = no step cap: the agent loops until the model gives a final answer,
			// the user cancels, or the provider errors. Context stays bounded by
			// compaction, not by a round count. Set a positive agent.max_steps only
			// if you want a hard guard against runaway.
			MaxSteps: 0,
		},
		// Mode "ask" with no rules keeps `ok run` autonomous (no TTY → ask
		// resolves to allow) while `ok chat` prompts before writers. Users add
		// deny/allow rules to harden or quiet specific tools.
		Permissions: PermissionsConfig{Mode: "ask"},
		// Sandbox on by default: bash is jailed (macOS), network allowed so
		// builds/downloads work. Set bash = "off" to disable. Network=true here
		// so an absent [sandbox] in a user's file keeps egress (zero value would
		// wrongly deny it).
		Sandbox: SandboxConfig{Bash: "enforce", Network: true},
		// CodeGraph code-intelligence on by default: when its bundle resolves it is
		// injected as a built-in MCP server. A missing bundle is a silent no-op, so
		// the default is safe even before the binary ships. Set enabled = false to
		// opt out entirely.
		Codegraph: CodegraphConfig{Enabled: true},
		Providers: []ProviderEntry{
			{Name: "deepseek-flash", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash", APIKeyEnv: "DEEPSEEK_API_KEY", BalanceURL: "https://api.deepseek.com/user/balance", ContextWindow: 1_000_000, Price: &provider.Pricing{CacheHit: 0.02, Input: 1, Output: 2, Currency: "¥"}},
			{Name: "deepseek-pro", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-pro", APIKeyEnv: "DEEPSEEK_API_KEY", BalanceURL: "https://api.deepseek.com/user/balance", ContextWindow: 1_000_000, Price: &provider.Pricing{CacheHit: 0.025, Input: 3, Output: 6, Currency: "¥"}},
			{Name: "mimo-pro", Kind: "openai", BaseURL: "https://api.xiaomimimo.com/v1", Model: "mimo-v2.5-pro", APIKeyEnv: "MIMO_API_KEY", ContextWindow: 1_000_000},
			{Name: "mimo-flash", Kind: "openai", BaseURL: "https://api.xiaomimimo.com/v1", Model: "mimo-v2-flash", APIKeyEnv: "MIMO_API_KEY", ContextWindow: 65_536},
			{Name: "claude-sonnet", Kind: "anthropic", BaseURL: "https://api.anthropic.com", Model: "claude-sonnet-4-20250514", APIKeyEnv: "ANTHROPIC_API_KEY", ContextWindow: 200_000, Price: &provider.Pricing{Input: 3, Output: 15, Currency: "$"}},
			{Name: "claude-haiku", Kind: "anthropic", BaseURL: "https://api.anthropic.com", Model: "claude-haiku-3-5-20241022", APIKeyEnv: "ANTHROPIC_API_KEY", ContextWindow: 200_000, Price: &provider.Pricing{Input: 0.80, Output: 4, Currency: "$"}},
			{Name: "gemini-flash", Kind: "openai", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Model: "gemini-2.5-flash", APIKeyEnv: "GEMINI_API_KEY", ContextWindow: 1_048_576},
			{Name: "gemini-pro", Kind: "openai", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Model: "gemini-2.5-pro", APIKeyEnv: "GEMINI_API_KEY", ContextWindow: 1_048_576},
		},
	}
}

// Load builds the configuration: defaults, then user config, then project
// config, then any MCP servers from Claude Code's .mcp.json. A .env in the
// working directory is loaded first so api_key_env can resolve.
func Load() (*Config, error) {
	loadDotEnv()
	cfg := Default()

	// Sensible default: 5 concurrent sub-agents matches typical API rate limits.
	if cfg.Agent.MaxConcurrentTasks <= 0 {
		cfg.Agent.MaxConcurrentTasks = 5
	}

	if uc := userConfigPath(); uc != "" {
		if err := mergeFile(cfg, uc); err != nil {
			return nil, err
		}
	}
	if err := mergeFile(cfg, "ok.toml"); err != nil {
		return nil, err
	}
	// Claude Code's .mcp.json (project root) is read last and merged into
	// [[plugins]], so a server configured for Claude works here unchanged.
	// ok.toml wins on a name collision (see mergeMCPJSON).
	entries, err := loadMCPJSON(mcpJSONFile)
	if err != nil {
		return nil, err
	}
	cfg.mergeMCPJSON(entries)

	// Auto-detect providers from environment variables: any well-known env var
	// that has a value and whose canonical provider name isn't already in the
	// config gets a [[providers]] entry appended. This means the user can set
	// OPENAI_API_KEY, ANTHROPIC_API_KEY, GROQ_API_KEY, etc. and have OK
	// Just Work™ with zero TOML editing.
	if auto := AutoDetectProviders(cfg.Providers); len(auto) > 0 {
		cfg.Providers = append(cfg.Providers, auto...)
	}

	// If the configured default_model has no key set but another provider does,
	// auto-switch the default to the detected provider. This makes the first-run
	// experience seamless: set any API key, run "ok", and it just works.
	AutoDetectDefaultModel(cfg)

	return cfg, nil
}

// LoadForEdit returns a config to seed the `ok setup` wizard when reconfiguring:
// the built-in defaults with the file at path (if present) decoded on top, so a
// reconfigure preserves the user's existing providers and agent settings instead
// of resetting to defaults. .env is loaded so api_key_env resolution works while
// the wizard decides which keys are still missing.
func LoadForEdit(path string) *Config {
	loadDotEnv()
	cfg := Default()
	if err := mergeFile(cfg, path); err != nil {
		fmt.Fprintf(os.Stderr, "config: merge file %s: %v\n", path, err)
	}
	return cfg
}

// mergeFile decodes a TOML file onto cfg if it exists. An absent file is not an error.
func mergeFile(cfg *Config, path string) error {
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("config %s: %w", path, err)
	}
	return nil
}

func userConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ok", "config.toml")
}

// WriteFile writes the configuration to path as annotated TOML.
func (c *Config) WriteFile(path string) error {
	return os.WriteFile(path, []byte(RenderTOML(c)), 0o644)
}
