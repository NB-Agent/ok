package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestRenderTOMLRoundTrips ensures the annotated TOML we emit parses back into
// an equivalent config — i.e. the wizard never writes a file it can't read.
func TestRenderTOMLRoundTrips(t *testing.T) {
	orig := Default()
	orig.DefaultModel = "mimo-pro"
	orig.Language = "zh"
	orig.Mode = ModeConfig{
		Default: "yolo",
		Deny:    []string{"bash(rm -rf*)"},
	}
	orig.Plugins = []PluginEntry{
		{Name: "example", Command: "ok-plugin-example"},
		{Name: "stripe", Type: "http", URL: "https://mcp.stripe.com", Headers: map[string]string{"Authorization": "Bearer x"}},
	}
	mm, _ := orig.Provider("mimo-pro")
	mm.BaseURL = "http://localhost:8000/v1"

	rendered := RenderTOML(orig)

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n---\n%s", err, rendered)
	}

	if got.DefaultModel != "mimo-pro" {
		t.Errorf("default_model = %q, want mimo-pro", got.DefaultModel)
	}
	if got.Language != "zh" {
		t.Errorf("language = %q, want zh", got.Language)
	}
	if got.Agent.MaxSteps != orig.Agent.MaxSteps {
		t.Errorf("max_steps = %d, want %d", got.Agent.MaxSteps, orig.Agent.MaxSteps)
	}
	if got.Agent.Temperature != orig.Agent.Temperature {
		t.Errorf("temperature = %v, want %v", got.Agent.Temperature, orig.Agent.Temperature)
	}
	if got.Agent.SystemPrompt != orig.Agent.SystemPrompt {
		t.Errorf("system_prompt mismatch:\n got %q\nwant %q", got.Agent.SystemPrompt, orig.Agent.SystemPrompt)
	}
	if g, _ := got.Provider("mimo-pro"); g == nil || g.BaseURL != "http://localhost:8000/v1" {
		t.Errorf("mimo-pro base_url not preserved: %+v", g)
	}
	if len(got.Providers) != len(orig.Providers) {
		t.Errorf("providers count = %d, want %d", len(got.Providers), len(orig.Providers))
	}
	if got.Mode.Default != "yolo" {
		t.Errorf("mode.default = %q, want yolo", got.Mode.Default)
	}
	if len(got.Mode.Deny) != 1 || got.Mode.Deny[0] != "bash(rm -rf*)" {
		t.Errorf("mode.deny = %v, want [bash(rm -rf*)]", got.Mode.Deny)
	}
	if len(got.Plugins) != 2 {
		t.Fatalf("plugins count = %d, want 2", len(got.Plugins))
	}
	stripe := got.Plugins[1]
	if stripe.Name != "stripe" || stripe.Type != "http" || stripe.URL != "https://mcp.stripe.com" {
		t.Errorf("http plugin not preserved: %+v", stripe)
	}
	if stripe.Headers["Authorization"] != "Bearer x" {
		t.Errorf("plugin headers not preserved: %v", stripe.Headers)
	}
}

// TestRenderTOMLMentionsEveryField walks the Config struct tree via reflection
// and verifies that every `toml:"..."` tagged field appears in the rendered
// output of a fully-populated config. A missing key means a new config field
// was added to the struct without a corresponding update to RenderTOML.
func TestRenderTOMLMentionsEveryField(t *testing.T) {
	// Build a fully-populated config so every conditional field renders.
	cfg := Default()
	cfg.Language = "zh"
	cfg.Agent.MaxSteps = 10
	cfg.Agent.Temperature = 0.7
	cfg.Agent.PlannerModel = "deepseek-pro"
	cfg.Agent.SystemPromptFile = "prompts/custom.md"
	cfg.Agent.MaxConcurrentTasks = 3
	cfg.Tools.Enabled = []string{"bash", "read_file"}
	cfg.Mode = ModeConfig{
		Default: "normal",
		Deny:    []string{"bash(rm *)"},
	}
	cfg.Sandbox = SandboxConfig{
		Bash:          "enforce",
		Network:       true,
		WorkspaceRoot: "/tmp/proj",
		AllowWrite:    []string{"/tmp"},
	}
	cfg.Plugins = []PluginEntry{{
		Name:    "p1",
		Command: "bin",
		Args:    []string{"-v"},
		Env:     map[string]string{"K": "V"},
		Headers: map[string]string{"A": "b"},
	}}
	// Add a provider using the vendor model-list path so `models` + `default`
	// appear in the rendered output.
	cfg.Providers = append(cfg.Providers, ProviderEntry{
		Name: "multi-model", Kind: "openai", BaseURL: "https://api.example.com",
		Models:  []string{"model-a", "model-b"},
		Default: "model-a",
	})
	cfg.Skills = SkillsConfig{Paths: []string{"/custom/skills"}}
	cfg.Codegraph = CodegraphConfig{Enabled: true, Path: "/opt/codegraph"}
	// Ensure provider has every optional field set.
	p, _ := cfg.Provider("deepseek-flash")
	p.BalanceURL = "https://api.deepseek.com/user/balance"
	p.ContextWindow = 100000

	out := RenderTOML(cfg)

	// Fields the renderer intentionally skips (e.g. back-compat aliases or
	// fields rendered only in a different section). Entries here are a strong
	// signal that the renderer needs an update.
	knownSkipped := map[string]bool{}

	missing := collectTOMLKeys(cfg, knownSkipped)
	for _, key := range missing {
		if !strings.Contains(out, key) {
			t.Errorf("rendered TOML missing %q — update RenderTOML or add to knownSkipped", key)
		}
	}
}

// collectTOMLKeys returns the set of leaf-level TOML key names that the
// renderer should be writing. It walks the Config struct tree via reflection
// and flattens every `toml:"..."` tag into a simple key (no section prefix)
// so the test can grep the rendered output for each key.
func collectTOMLKeys(v any, skip map[string]bool) []string {
	var keys []string
	seen := map[string]bool{}
	structType := reflect.TypeOf(v)
	if structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}
	walkTOML(structType, "", skip, &keys, seen)
	return keys
}

func walkTOML(t reflect.Type, section string, skip map[string]bool, keys *[]string, seen map[string]bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, ok := f.Tag.Lookup("toml")
		if !ok || tag == "-" {
			continue
		}
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			tag = tag[:comma]
		}
		full := tag
		if section != "" {
			full = section + "." + tag
		}
		if skip[full] {
			continue
		}

		ft := f.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		if ft.Kind() == reflect.Struct && ft.PkgPath() != "github.com/NB-Agent/ok/internal/config" {
			// External type (e.g. provider.Pricing) — emit parent tag only.
			if !seen[tag] {
				seen[tag] = true
				*keys = append(*keys, tag)
			}
			continue
		}

		switch ft.Kind() {
		case reflect.Struct:
			walkTOML(ft, full, skip, keys, seen)
		case reflect.Slice:
			if ft.Elem().Kind() == reflect.Struct {
				walkTOML(ft.Elem(), full, skip, keys, seen)
			} else if !seen[tag] {
				seen[tag] = true
				*keys = append(*keys, tag)
			}
		default:
			if !seen[tag] {
				seen[tag] = true
				*keys = append(*keys, tag)
			}
		}
	}
}
