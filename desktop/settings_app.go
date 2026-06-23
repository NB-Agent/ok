package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/boot"
	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/i18n"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/winhide"
)

// settings_app.go is the desktop Settings panel's command surface: it reads the
// resolved config and applies edits through internal/config/edit.go (the
// purpose-built mutation API), then rebuilds the controller so the change takes
// effect live — the same snapshot→reload→resume pattern as SetModel. Secrets are
// the exception: they go to ./.env (upsertDotEnv), since config stores only the
// env-var name, not the key.

// --- read ---

type ProviderView struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	BaseURL       string   `json:"baseUrl"`
	Models        []string `json:"models"`
	Default       string   `json:"default"`
	APIKeyEnv     string   `json:"apiKeyEnv"`
	KeySet        bool     `json:"keySet"` // the env var currently resolves to a non-empty value
	BalanceURL    string   `json:"balanceUrl"`
	ContextWindow int      `json:"contextWindow"`
}

type PermissionsView struct {
	Mode  string   `json:"mode"`
	Deny  []string `json:"deny"`
	Allow []string `json:"allow"`
	Ask   []string `json:"ask"`
}

type SandboxView struct {
	Bash    string `json:"bash"`
	Network bool   `json:"network"`
}

type AgentView struct {
	Temperature  float64 `json:"temperature"`
	MaxSteps     int     `json:"maxSteps"`
}

type PluginView struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Command string `json:"command"`
	Args    string `json:"args"`
	URL     string `json:"url"`
	KeySet  bool   `json:"keySet"`
}

type RouterView struct {
	Enabled        bool   `json:"enabled"`
	CheapModel     string `json:"cheapModel"`
	ExpensiveModel string `json:"expensiveModel"`
}

// SettingsView is the whole Settings panel payload.
type SettingsView struct {
	DefaultModel string          `json:"defaultModel"`
	PlannerModel string          `json:"plannerModel"`
	Agent        AgentView       `json:"agent"`
	Providers    []ProviderView  `json:"providers"`
	Permissions  PermissionsView `json:"permissions"`
	Sandbox      SandboxView     `json:"sandbox"`
	Plugins      []PluginView    `json:"plugins"`
	Router       RouterView      `json:"router"`
	Language     string          `json:"language"`
	ConfigPath   string          `json:"configPath"`
	ProviderKinds []string       `json:"providerKinds"`
	Bypass       bool            `json:"bypass"`
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Settings returns the current configuration for the Settings panel.
func (a *App) Settings() SettingsView {
	cfg, err := config.Load()
	if err != nil {
		return SettingsView{Providers: []ProviderView{}}
	}
	bash := cfg.Sandbox.Bash
	if bash == "" {
		bash = "enforce"
	}
	v := SettingsView{
		DefaultModel: cfg.DefaultModel,
		PlannerModel: cfg.Agent.PlannerModel,
		Agent: AgentView{
			Temperature: cfg.Agent.Temperature,
			MaxSteps:    cfg.Agent.MaxSteps,
		},
		Providers:    []ProviderView{},
		Permissions: PermissionsView{
			Mode:  cfg.ModeStyle(),
			Deny:  nonNil(cfg.ModeDeny()),
			Allow: nonNil(cfg.ModeAllow()),
			Ask:   nonNil(cfg.ModeAsk()),
		},
		Sandbox: SandboxView{
			Bash: bash, Network: cfg.Sandbox.Network,
		},
		Plugins:       []PluginView{},
		Router:        RouterView{Enabled: cfg.Router.Enabled, CheapModel: cfg.Router.CheapModel, ExpensiveModel: cfg.Router.ExpensiveModel},
		Language:      cfg.Language,
		ConfigPath:    config.SourcePath(),
		ProviderKinds: provider.Kinds(),
		Bypass:        a.ctrl != nil && a.ctrl.Bypass(),
	}
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		v.Providers = append(v.Providers, ProviderView{
			Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL,
			Models: nonNil(p.ModelList()), Default: p.DefaultModel(),
			APIKeyEnv:     p.APIKeyEnv,
			KeySet:        p.APIKeyEnv != "" && os.Getenv(p.APIKeyEnv) != "",
			BalanceURL:    p.BalanceURL,
			ContextWindow: p.ContextWindow,
		})
	}
	for i := range cfg.Plugins {
		p := &cfg.Plugins[i]
		keySet := false
		for _, env := range p.Env {
			if strings.HasPrefix(env, "$") && os.Getenv(strings.TrimPrefix(env, "$")) != "" {
				keySet = true
				break
			}
		}
		v.Plugins = append(v.Plugins, PluginView{
			Name: p.Name, Type: p.Type, Command: p.Command,
			Args: strings.Join(p.Args, " "), URL: p.URL, KeySet: keySet,
		})
	}
	return v
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// --- apply (write config, then rebuild the controller so it's live) ---

// applyConfigChange loads the config, applies mutate, saves it, and rebuilds the
// controller so the change takes effect this session.
func (a *App) applyConfigChange(mutate func(*config.Config) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := mutate(cfg); err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	return a.rebuild()
}

// rebuild tears down the controller and rebuilds it from the (just-changed)
// config, carrying the conversation forward. It keeps the active model if it
// still resolves; otherwise it falls back to the new default. Mirrors SetModel.
func (a *App) rebuild() error {
	if a.ctx == nil {
		return nil
	}
	var carried []provider.Message
	if a.ctrl != nil {
		if err := a.ctrl.Snapshot(); err != nil {
			fmt.Fprintf(os.Stderr, "desktop: settings rebuild snapshot: %v\n", err)
		}
		carried = a.ctrl.History()
		a.ctrl.Close()
	}
	model := a.model
	if cfg, err := config.Load(); err == nil {
		if _, ok := cfg.ResolveModel(model); !ok {
			model = cfg.DefaultModel
			if e, ok := cfg.ResolveModel(model); ok {
				model = e.Name + "/" + e.Model
			}
		}
	}
	ctrl, err := boot.Build(a.ctx, boot.Options{Model: model, RequireKey: false, Sink: a.sink})
	if err != nil {
		a.ctrl = nil
		a.startupErr = err.Error()
		return err
	}
	a.ctrl = ctrl
	a.model = model
	a.label = ctrl.Label()
	a.startupErr = ""
	ctrl.EnableInteractiveApproval()
	path := ""
	if dir := ctrl.SessionDir(); dir != "" {
		path = agent.NewSessionPath(dir, ctrl.Label())
	}
	if len(carried) > 0 {
		ctrl.Resume(&agent.Session{Messages: carried}, path)
	} else if path != "" {
		ctrl.SetSessionPath(path)
	}
	return nil
}

// SetDefaultModel sets the config default and switches the live model to it.
func (a *App) SetDefaultModel(ref string) error {
	prev := a.model
	a.model = ref
	if err := a.applyConfigChange(func(c *config.Config) error {
		if _, ok := c.ResolveModel(ref); !ok {
			return fmt.Errorf("unknown model %q", ref)
		}
		c.DefaultModel = ref
		return nil
	}); err != nil {
		a.model = prev
		return err
	}
	return nil
}

// SetPlannerModel sets (or, with "", clears) the two-model planner.
func (a *App) SetPlannerModel(ref string) error {
	return a.applyConfigChange(func(c *config.Config) error {
		if ref != "" {
			if _, ok := c.ResolveModel(ref); !ok {
				return fmt.Errorf("unknown planner model %q", ref)
			}
		}
		c.Agent.PlannerModel = ref
		return nil
	})
}

// SaveProvider adds or updates a provider. A single model fills `model`; several
// fill `models` (with `default`). The shared key/endpoint live on the entry.
func (a *App) SaveProvider(p ProviderView) error {
	return a.applyConfigChange(func(c *config.Config) error {
		e := config.ProviderEntry{
			Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL,
			APIKeyEnv: p.APIKeyEnv, BalanceURL: strings.TrimSpace(p.BalanceURL), ContextWindow: p.ContextWindow,
		}
		if len(p.Models) > 0 {
			e.Model = p.Models[0] // also satisfies validateProvider's model requirement
			if len(p.Models) > 1 {
				e.Models = p.Models
				e.Default = p.Default
			}
		}
		return c.UpsertProvider(e)
	})
}

// DeleteProvider removes a provider (refused for the current default_model).
func (a *App) DeleteProvider(name string) error {
	return a.applyConfigChange(func(c *config.Config) error { return c.RemoveProvider(name) })
}

// SetProviderKey writes a secret to ./.env under the given env-var name (the one a
// provider's api_key_env points at) and rebuilds so it resolves immediately.
func (a *App) SetProviderKey(apiKeyEnv, value string) error {
	if strings.TrimSpace(apiKeyEnv) == "" {
		return fmt.Errorf("this provider has no api_key_env set")
	}
	if err := upsertDotEnv(apiKeyEnv, value); err != nil {
		return err
	}
	return a.rebuild()
}

// SetModeStyle sets the interaction style (plan|normal|yolo).
func (a *App) SetModeStyle(style string) error {
	return a.applyConfigChange(func(c *config.Config) error { return c.SetModeStyle(style) })
}

// SetPermissionMode maps old permission mode names to new mode style.
// Deprecated: use SetModeStyle instead.
func (a *App) SetPermissionMode(mode string) error {
	switch mode {
	case "allow", "deny":
		return a.SetModeStyle("yolo")
	case "ask":
		return a.SetModeStyle("normal")
	default:
		return a.SetModeStyle(mode)
	}
}

// AddPermissionRule appends a rule to the deny list.
func (a *App) AddPermissionRule(list, rule string) error {
	return a.applyConfigChange(func(c *config.Config) error { return c.AddPermissionRule(list, rule) })
}

// RemovePermissionRule drops a rule from the allow/ask/deny list.
func (a *App) RemovePermissionRule(list, rule string) error {
	return a.applyConfigChange(func(c *config.Config) error {
		_, err := c.RemovePermissionRule(list, rule)
		return err
	})
}

// SetSandbox updates the bash sandbox mode and network egress.
func (a *App) SetSandbox(bash string, network bool) error {
	return a.applyConfigChange(func(c *config.Config) error {
		c.Sandbox.Bash = bash
		c.Sandbox.Network = network
		return nil
	})
}

// SetAgentParams updates sampling temperature, the optional max-steps guard, and
// the base system prompt.
func (a *App) SetAgentParams(temperature float64, maxSteps int, systemPrompt string) error {
	return a.applyConfigChange(func(c *config.Config) error {
		c.Agent.Temperature = temperature
		c.Agent.MaxSteps = maxSteps
		c.Agent.SystemPrompt = systemPrompt
		return nil
	})
}

// SetLanguage sets the UI language tag ("zh" | "en" | "" for auto). It only
// rewrites config — no controller rebuild needed.
func (a *App) SetLanguage(lang string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Language = strings.TrimSpace(lang)
	// Keep the Go-side catalog in sync so backend-provided slash UI re-localizes
	// on the next fetch (matches the frontend's language switch).
	i18n.DetectLanguage(cfg.Language)
	return cfg.Save()
}

// SavePlugin adds or updates an MCP plugin server.
func (a *App) SavePlugin(p PluginView) error {
	return a.applyConfigChange(func(c *config.Config) error {
		return c.UpsertPlugin(config.PluginEntry{
			Name: p.Name, Type: p.Type, Command: p.Command,
			Args: strings.Fields(p.Args), URL: p.URL,
		})
	})
}

// DeletePlugin removes a named MCP server.
func (a *App) DeletePlugin(name string) error {
	return a.applyConfigChange(func(c *config.Config) error {
		if !c.RemovePlugin(name) {
			return fmt.Errorf("plugin %q not found", name)
		}
		return nil
	})
}

// SetRouter enables/disables model routing and sets cheap/expensive models.
func (a *App) SetRouter(enabled bool, cheap, expensive string) error {
	return a.applyConfigChange(func(c *config.Config) error {
		c.Router.Enabled = enabled
		c.Router.CheapModel = cheap
		c.Router.ExpensiveModel = expensive
		return nil
	})
}

// trimList drops blank entries from a string slice (and returns a non-nil slice).
func trimList(in []string) []string {
	out := []string{}
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ─── Bot management ──────────────────────────────────────────────────────────

// BotStatus reports a bot binary's configured state.
type BotStatus struct {
	Name     string `json:"name"`
	KeySet   bool   `json:"keySet"`   // credentials saved
	Running  bool   `json:"running"`  // process alive
}

// RunningBots returns the status of all known bot platforms.
func (a *App) RunningBots() []BotStatus {
	envVars := []struct{
		name string
		env  string
	}{
		{"Slack", "SLACK_BOT_TOKEN"},
		{"Discord", "DISCORD_BOT_TOKEN"},
		{"Telegram", "TELEGRAM_BOT_TOKEN"},
		{"企业微信", "WECHAT_CORP_ID"},
		{"飞书", "FEISHU_APP_ID"},
		{"WhatsApp", "WHATSAPP_PHONE_ID"},
		{"钉钉", "DINGTALK_WEBHOOK_URL"},
	}
	a.botsMu.Lock()
	defer a.botsMu.Unlock()
	out := make([]BotStatus, 0, len(envVars))
	for _, ev := range envVars {
		_, running := a.botCmds[ev.name]
		out = append(out, BotStatus{
			Name:    ev.name,
			KeySet:  os.Getenv(ev.env) != "",
			Running: running,
		})
	}
	return out
}

// SetBotEnv saves a bot credential to .env (same pattern as SetProviderKey).
func (a *App) SetBotEnv(key, value string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("env var name is required")
	}
	return upsertDotEnv(key, value)
}

// findCLIBin locates the ok-cli.exe for bot processes.
func findCLIBin() string {
	candidates := []string{"ok", "ok.exe", "ok-cli.exe"}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		candidates = append([]string{
			filepath.Join(d, "ok-cli.exe"), filepath.Join(d, "ok.exe"),
			filepath.Join(d, "bin", "ok-cli.exe"), filepath.Join(d, "bin", "ok.exe"),
			filepath.Join("build", "bin", "ok-cli.exe"), filepath.Join("build", "bin", "ok.exe"),
		}, candidates...)
	}
	for _, c := range candidates {
		if p, e := exec.LookPath(c); e == nil {
			if a, e := filepath.Abs(p); e == nil { return a }
		}
		if _, e := os.Stat(c); e == nil {
			if a, e := filepath.Abs(c); e == nil { return a }
		}
	}
	return "ok"
}

// StartBot launches a bot binary with -ok-bin -ok-model -work-dir.
func (a *App) StartBot(name string) error {
	binMap := map[string]string{
		"Slack": "ok-slack-bot", "Discord": "ok-discord-bot", "Telegram": "ok-telegram-bot",
		"企业微信": "ok-wechat-bot", "飞书": "ok-feishu-bot",
		"WhatsApp": "ok-whatsapp-bot", "钉钉": "ok-dingtalk-bot",
	}
	bin, ok := binMap[name]
	if !ok { return fmt.Errorf("unknown bot %q", name) }
	a.botsMu.Lock()
	defer a.botsMu.Unlock()
	if _, running := a.botCmds[name]; running {
		return fmt.Errorf("bot %q is already running", name)
	}
	// Resolve ok-cli path and model from config
	okBin := findCLIBin()
	model := ""
	if cfg, err := config.Load(); err == nil {
		model = cfg.DefaultModel
		if e, ok := cfg.ResolveModel(cfg.DefaultModel); ok {
			model = e.Name + "/" + e.Model
		}
	}
	workDir := "."
	if d, err := os.Getwd(); err == nil { workDir = d }

	args := []string{"-ok-bin", okBin, "-ok-model", model, "-work-dir", workDir}
	exeDir := ""
	if exe, err := os.Executable(); err == nil { exeDir = filepath.Dir(exe) }
	var cmd *exec.Cmd
	found := false
	for _, p := range []string{
		filepath.Join(exeDir, "bin", bin+".exe"),
		filepath.Join("build", "bin", bin+".exe"),
		filepath.Join(exeDir, bin+".exe"),
		bin + ".exe",
	} {
		if _, err := os.Stat(p); err == nil {
			cmd = winhide.CommandContext(context.Background(), p, args...)
			found = true; break
		}
	}
	if !found { cmd = winhide.CommandContext(context.Background(), bin, args...) }
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}
	a.botCmds[name] = cmd
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "settings: bot %q wait panic: %v\n", name, r)
			}
		}()
		cmd.Wait()
		a.botsMu.Lock()
		delete(a.botCmds, name)
		a.botsMu.Unlock()
	}()
	return nil
}

// StopBot kills a running bot process.
func (a *App) StopBot(name string) error {
	a.botsMu.Lock()
	defer a.botsMu.Unlock()
	cmd, ok := a.botCmds[name]
	if !ok {
		return fmt.Errorf("bot %q is not running", name)
	}
	cmd.Process.Kill()
	delete(a.botCmds, name)
	return nil
}
