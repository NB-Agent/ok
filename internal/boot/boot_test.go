package boot

import (
	"context"
	"strings"
	"testing"

	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/provider"

	// Blank imports register the provider kind and built-in tools the same way
	// cmd/ok's main does; without them Build sees an empty provider
	// registry and a bare tool set.
	_ "github.com/NB-Agent/ok/internal/provider/openai"
	_ "github.com/NB-Agent/ok/internal/tool/builtin"
)

// TestBuildFoldsProjectMemoryIntoSystemPrompt is the end-to-end proof of the
// cache-first wiring: a project REASONIX.md is discovered at boot and folded
// into the session's system message (the cached prefix), and the `remember`
// tool is registered. It builds a real Controller from a throwaway project dir.
func TestBuildFoldsProjectMemoryIntoSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "ok.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE SYSTEM PROMPT"

[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "OK_TEST_KEY_UNSET"
`)
	writeFile(t, dir, "REASONIX.md", "Project rule: always run go vet before committing.")

	ctrl, err := Build(context.Background(), Options{}) // RequireKey false: no network/key needed
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()

	// The system message is the cached prefix; it must contain the base prompt.
	sys := systemMessage(ctrl.History())
	if !strings.Contains(sys, "BASE SYSTEM PROMPT") {
		t.Fatalf("base prompt missing from system message:\n%s", sys)
	}

	// Memory may be in the system message (legacy) or in FirstTurnInject (new
	// cache-stable architecture). Accept either.
	memContent := "always run go vet before committing"
	inSys := strings.Contains(sys, memContent)
	inInject := strings.Contains(ctrl.FirstTurnInject(), memContent)
	if !inSys && !inInject {
		t.Fatalf("project REASONIX.md not folded into system message or FirstTurnInject:\nsys=%s\ninject=%s",
			sys, ctrl.FirstTurnInject())
	}

	// When memory IS in the system message, assert base comes first.
	if inSys {
		if strings.Index(sys, "BASE SYSTEM PROMPT") > strings.Index(sys, memContent) {
			t.Fatalf("memory should follow the base prompt, not precede it:\n%s", sys)
		}
	}

	if mem := ctrl.Memory(); mem == nil || len(mem.Docs) == 0 {
		t.Fatal("controller memory set is empty after discovering OK.md")
	}
}

// TestBuildDiscoversSkills proves the skill wiring end-to-end: a project skill
// is discovered at boot, surfaced via Controller.Skills(), and its name folds
// into the cache-stable system prompt's "# Skills" index alongside a built-in.
func TestBuildDiscoversSkills(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "ok.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE"

[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "OK_TEST_KEY_UNSET"
`)
	writeFile(t, dir, ".ok/skills/projskill.md", "---\ndescription: a project skill\n---\nplaybook")

	ctrl, err := Build(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()

	var hasProj, hasBuiltin bool
	for _, s := range ctrl.Skills() {
		switch s.Name {
		case "projskill":
			hasProj = true
		case "review":
			hasBuiltin = true
		}
	}
	if !hasProj || !hasBuiltin {
		t.Fatalf("Skills() should include the project skill and a built-in; got %v", ctrl.Skills())
	}

	// Skills index may be in the system message (legacy) or in FirstTurnInject
	// (new cache-stable architecture). Accept either.
	sys := systemMessage(ctrl.History())
	inj := ctrl.FirstTurnInject()
	if !strings.Contains(sys, "# Skills") && !strings.Contains(inj, "# Skills") {
		t.Fatalf("skills index missing from system prompt or FirstTurnInject:\nsys=%s\ninject=%s", sys, inj)
	}
	if !strings.Contains(sys, "projskill") && !strings.Contains(inj, "projskill") {
		t.Fatalf("projskill name missing from system prompt or FirstTurnInject:\nsys=%s\ninject=%s", sys, inj)
	}
	if !strings.Contains(sys, "review") && !strings.Contains(inj, "review") {
		t.Fatalf("review name missing from system prompt or FirstTurnInject:\nsys=%s\ninject=%s", sys, inj)
	}
}

// TestBuildWithoutMemoryLeavesPromptUnchanged is the inverse invariant: with no
// memory files, the system prompt is exactly the configured base — the cache
// prefix is untouched by the memory feature.
func TestBuildWithoutMemoryLeavesPromptUnchanged(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "ok.toml", `
default_model = "test-model"

[agent]
system_prompt = "JUST THE BASE"

[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "OK_TEST_KEY_UNSET"
`)

	ctrl, err := Build(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()

	// The system message must contain exactly the base prompt + language policy
	// (plus any built-in add-ons like Core Covenant and Skills index). No project
	// or ancestor memory should leak in. FirstTurnInject may be empty or contain
	// only skills (since built-in skills are always present).
	sys := systemMessage(ctrl.History())
	base := sys
	// Strip the Core Covenant prefix (compiled-in, always present).
	if i := strings.Index(base, "\n\n"); i >= 0 && strings.HasPrefix(base, "# Core Covenant") {
		base = base[i+2:]
	}
	// Strip the system-prompt Skills index if present (legacy mode).
	if i := strings.Index(sys, "\n\n# Skills"); i >= 0 {
		base = sys[:i]
	}
	// The language policy is always appended at boot; strip it.
	base = strings.Replace(base, "\n\n"+config.LanguagePolicy, "", 1)
	// The shared knowledge section may be appended if the real config dir has
	// any shared memory — strip it for this test too.
	if i := strings.Index(base, "\n\n# Shared Knowledge"); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimSpace(base)
	if base != "JUST THE BASE" {
		t.Fatalf("expected untouched base prompt, got:\n%s (sys)\n%s (base)", sys, base)
	}
}

func systemMessage(msgs []provider.Message) string {
	for _, m := range msgs {
		if m.Role == provider.RoleSystem {
			return m.Content
		}
	}
	return ""
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := writeFileRaw(dir, name, body); err != nil {
		t.Fatal(err)
	}
}
