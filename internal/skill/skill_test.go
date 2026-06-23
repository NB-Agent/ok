package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, base, rel, content string) string {
	t.Helper()
	full := filepath.Join(base, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func find(skills []Skill, name string) (Skill, bool) {
	for _, s := range skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

func TestListPrecedenceProjectOverGlobal(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	writeSkill(t, proj, ".ok/skills/greet.md", "---\nname: greet\ndescription: project greet\n---\nproject body")
	writeSkill(t, home, ".ok/skills/greet.md", "---\ndescription: global greet\n---\nglobal body")
	writeSkill(t, home, ".ok/skills/onlyglobal.md", "---\ndescription: only global\n---\nbody")

	st := New(Options{HomeDir: home, ProjectRoot: proj, DisableBuiltins: true})
	list := st.List()

	greet, ok := find(list, "greet")
	if !ok {
		t.Fatal("greet not found")
	}
	if greet.Scope != ScopeProject || greet.Description != "project greet" {
		t.Fatalf("project skill should win: got scope=%s desc=%q", greet.Scope, greet.Description)
	}
	if _, ok := find(list, "onlyglobal"); !ok {
		t.Fatal("global-only skill should be discovered")
	}
}

func TestFlatAndDirLayout(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, ".ok/skills/flat.md", "---\ndescription: flat\n---\nflat body")
	writeSkill(t, home, ".ok/skills/dir/SKILL.md", "---\ndescription: dir\n---\ndir body")

	st := New(Options{HomeDir: home, DisableBuiltins: true})
	list := st.List()
	if _, ok := find(list, "flat"); !ok {
		t.Error("flat <name>.md skill not discovered")
	}
	if _, ok := find(list, "dir"); !ok {
		t.Error("dir/SKILL.md skill not discovered")
	}
}

func TestConventionDirsDiscovered(t *testing.T) {
	proj := t.TempDir()
	writeSkill(t, proj, ".claude/skills/fromclaude.md", "---\ndescription: c\n---\nb")
	writeSkill(t, proj, ".agents/skills/fromagents.md", "---\ndescription: a\n---\nb")
	writeSkill(t, proj, ".agent/skills/fromagent.md", "---\ndescription: s\n---\nb")
	st := New(Options{HomeDir: t.TempDir(), ProjectRoot: proj, DisableBuiltins: true})
	list := st.List()
	for _, name := range []string{"fromclaude", "fromagents", "fromagent"} {
		if _, ok := find(list, name); !ok {
			t.Errorf("convention dir for %q not scanned", name)
		}
	}
}

func TestFrontmatterFields(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, ".ok/skills/sub.md",
		"---\ndescription: a sub\nrunAs: subagent\nallowed-tools: read_file, grep\nmodel: deepseek-pro\n---\nbody")
	writeSkill(t, home, ".ok/skills/fork.md", "---\ndescription: f\ncontext: fork\n---\nbody")
	writeSkill(t, home, ".ok/skills/plain.md", "---\ndescription: p\n---\nbody")

	st := New(Options{HomeDir: home, DisableBuiltins: true})
	sub, _ := st.Read("sub")
	if sub.RunAs != RunSubagent {
		t.Error("runAs: subagent not parsed")
	}
	if len(sub.AllowedTools) != 2 || sub.AllowedTools[0] != "read_file" || sub.AllowedTools[1] != "grep" {
		t.Errorf("allowed-tools mis-parsed: %v", sub.AllowedTools)
	}
	if sub.Model != "deepseek-pro" {
		t.Errorf("model mis-parsed: %q", sub.Model)
	}
	if fork, _ := st.Read("fork"); fork.RunAs != RunSubagent {
		t.Error("context: fork should imply subagent")
	}
	if plain, _ := st.Read("plain"); plain.RunAs != RunInline {
		t.Error("default runAs should be inline")
	}
}

func TestReferencesInlined(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, ".ok/skills/withrefs/SKILL.md", "---\ndescription: r\n---\nmain body")
	writeSkill(t, home, ".ok/skills/withrefs/references/b.md", "second ref")
	writeSkill(t, home, ".ok/skills/withrefs/references/a.md", "first ref")

	st := New(Options{HomeDir: home, DisableBuiltins: true})
	sk, ok := st.Read("withrefs")
	if !ok {
		t.Fatal("skill not found")
	}
	if !strings.Contains(sk.Body, "main body") {
		t.Error("main body missing")
	}
	// references are appended sorted by filename: a before b.
	ai := strings.Index(sk.Body, "## Reference: a")
	bi := strings.Index(sk.Body, "## Reference: b")
	if ai < 0 || bi < 0 || ai > bi {
		t.Errorf("references not appended in sorted order: a=%d b=%d", ai, bi)
	}
	if !strings.Contains(sk.Body, "first ref") || !strings.Contains(sk.Body, "second ref") {
		t.Error("reference contents missing")
	}
}

func TestBuiltinInitIsInlineSkill(t *testing.T) {
	// /init must resolve to a built-in inline skill (the model-driven AGENTS.md
	// bootstrap), present even with no project/user skills on disk.
	st := New(Options{HomeDir: t.TempDir()})
	sk, ok := st.Read("init")
	if !ok {
		t.Fatal("built-in init skill not found")
	}
	if sk.Scope != ScopeBuiltin || sk.RunAs != RunInline {
		t.Errorf("init should be a builtin inline skill, got scope=%s runAs=%s", sk.Scope, sk.RunAs)
	}
	if _, listed := find(st.List(), "init"); !listed {
		t.Error("init should appear in List() so it reaches the slash menu")
	}
}

func TestBuiltinsPresentAndOverridable(t *testing.T) {
	st := New(Options{HomeDir: t.TempDir()})
	if _, ok := find(st.List(), "review"); !ok {
		t.Error("built-in review should be present")
	}
	// A user file named after a built-in overrides it.
	home := t.TempDir()
	writeSkill(t, home, ".ok/skills/review.md", "---\ndescription: mine\nrunAs: inline\n---\nbody")
	st2 := New(Options{HomeDir: home})
	ex, _ := st2.Read("review")
	if ex.Scope == ScopeBuiltin || ex.Description != "mine" {
		t.Errorf("user review should override builtin: scope=%s desc=%q", ex.Scope, ex.Description)
	}
}

func TestInvalidNamesSkipped(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, ".ok/skills/bad name.md", "---\ndescription: x\n---\nb") // space → invalid
	st := New(Options{HomeDir: home, DisableBuiltins: true})
	if len(st.List()) != 0 {
		t.Errorf("invalid-named skill should be skipped, got %d", len(st.List()))
	}
}

func TestSymlinkedDirAndFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privilege on Windows")
	}
	home := t.TempDir()
	target := t.TempDir()
	// real skill dir + flat file living outside the skills root
	writeSkill(t, target, "realdir/SKILL.md", "---\ndescription: linked dir\n---\nb")
	writeSkill(t, target, "realflat.md", "---\ndescription: linked flat\n---\nb")

	skillsRoot := filepath.Join(home, ".ok", "skills")
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(target, "realdir"), filepath.Join(skillsRoot, "linkeddir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(target, "realflat.md"), filepath.Join(skillsRoot, "linkedflat.md")); err != nil {
		t.Fatal(err)
	}

	st := New(Options{HomeDir: home, DisableBuiltins: true})
	list := st.List()
	if _, ok := find(list, "linkeddir"); !ok {
		t.Error("symlinked skill directory not discovered")
	}
	if _, ok := find(list, "linkedflat"); !ok {
		t.Error("symlinked flat skill file not discovered")
	}
	// broken symlink is skipped, not fatal.
	if err := os.Symlink(filepath.Join(target, "does-not-exist"), filepath.Join(skillsRoot, "broken")); err != nil {
		t.Fatal(err)
	}
	if _, ok := find(st.List(), "broken"); ok {
		t.Error("broken symlink should not yield a skill")
	}
}

func TestApplyIndex(t *testing.T) {
	if got := ApplyIndex("BASE", nil); got != "BASE" {
		t.Errorf("empty skills should leave base unchanged, got %q", got)
	}
	skills := []Skill{
		{Name: "alpha", Description: "the alpha", RunAs: RunInline},
		{Name: "beta", Description: "the beta", RunAs: RunSubagent},
	}
	out := ApplyIndex("BASE", skills)
	if !strings.HasPrefix(out, "BASE\n\n# Skills") {
		t.Error("index should append after the base")
	}
	if !strings.Contains(out, "- alpha — the alpha") {
		t.Errorf("inline skill line missing: %s", out)
	}
	if !strings.Contains(out, "- beta [🧬 subagent] — the beta") {
		t.Errorf("subagent tag missing: %s", out)
	}
}

func TestApplyIndexTruncates(t *testing.T) {
	var skills []Skill
	for i := 0; i < 200; i++ {
		skills = append(skills, Skill{Name: "skill" + strings.Repeat("x", 20), Description: strings.Repeat("d", 50)})
	}
	out := ApplyIndex("BASE", skills)
	if !strings.Contains(out, "truncated") {
		t.Error("oversized index should be truncated")
	}
}

func TestCreateRefusesOverwrite(t *testing.T) {
	home := t.TempDir()
	st := New(Options{HomeDir: home, DisableBuiltins: true})
	path, err := st.Create("mine", ScopeGlobal)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join(".ok", "skills", "mine.md")) {
		t.Errorf("unexpected path %q", path)
	}
	if _, err := st.Create("mine", ScopeGlobal); err == nil {
		t.Error("second create should refuse to overwrite")
	}
}

func TestCreateInvalidName(t *testing.T) {
	home := t.TempDir()
	st := New(Options{HomeDir: home, DisableBuiltins: true})
	if _, err := st.Create("bad name!", ScopeGlobal); err == nil {
		t.Error("create with invalid name should error")
	}
}

func TestReadInvalidName(t *testing.T) {
	st := New(Options{HomeDir: t.TempDir(), DisableBuiltins: true})
	if _, ok := st.Read("has spaces!"); ok {
		t.Error("Read with invalid name should return false")
	}
}

func TestInstallSkillMissingName(t *testing.T) {
	tl := NewInstallSkillTool(New(Options{HomeDir: t.TempDir(), DisableBuiltins: true}), nil)
	_, err := tl.Execute(context.Background(), json.RawMessage(`{"name":"","description":"x","body":"b"}`))
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("want name error, got %v", err)
	}
}

func TestInstallSkillMissingBody(t *testing.T) {
	tl := NewInstallSkillTool(New(Options{HomeDir: t.TempDir(), DisableBuiltins: true}), nil)
	_, err := tl.Execute(context.Background(), json.RawMessage(`{"name":"ok","description":"x","body":""}`))
	if err == nil || !strings.Contains(err.Error(), "body") {
		t.Errorf("want body error, got %v", err)
	}
}

func TestInstallSkillInstalledHook(t *testing.T) {
	home := t.TempDir()
	var hookName, hookPath string
	var hookScope Scope
	st := New(Options{HomeDir: home, DisableBuiltins: true})
	tl := NewInstallSkillTool(st, func(name, path string, scope Scope) {
		hookName = name
		hookPath = path
		hookScope = scope
	})
	if _, err := tl.Execute(context.Background(), json.RawMessage(
		`{"name":"hooked","description":"d","body":"b"}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if hookName != "hooked" {
		t.Errorf("hook name = %q", hookName)
	}
	if hookPath == "" {
		t.Error("hook path should be set")
	}
	if hookScope != ScopeGlobal {
		t.Errorf("hook scope = %s", hookScope)
	}
}

func TestRenderOutput(t *testing.T) {
	sk := Skill{Name: "my-skill", Description: "does things", Scope: ScopeProject, Path: "/tmp/proj/.ok/skills/my-skill.md", Body: "Do the thing.", RunAs: RunInline}
	out := Render(sk, "extra args")
	if !strings.HasPrefix(out, "# Skill: my-skill") {
		t.Errorf("Render missing header: %s", out)
	}
	if !strings.Contains(out, "Do the thing.") {
		t.Error("Render missing body")
	}
	if !strings.Contains(out, "Arguments: extra args") {
		t.Error("Render missing arguments")
	}
}

func TestRenderSkillFile(t *testing.T) {
	content := renderSkillFile("deploy", "ship it", "step1\nstep2", RunSubagent, "deepseek-pro", []string{"bash", "read_file"})
	if !strings.Contains(content, "runAs: subagent") {
		t.Error("missing runAs: subagent")
	}
	if !strings.Contains(content, "model: deepseek-pro") {
		t.Error("missing model")
	}
	if !strings.Contains(content, "allowed-tools: bash, read_file") {
		t.Errorf("missing allowed-tools: %s", content)
	}
	if !strings.Contains(content, "description: ship it") {
		t.Error("missing description")
	}
	// inline mode should omit subagent-only fields
	inline := renderSkillFile("note", "x", "b", RunInline, "m", []string{"t"})
	if strings.Contains(inline, "runAs:") || strings.Contains(inline, "model:") || strings.Contains(inline, "allowed-tools:") {
		t.Errorf("inline render should omit subagent fields:\n%s", inline)
	}
}

func TestSubagentSkillToolRequiresRunner(t *testing.T) {
	tl := &subagentSkillTool{
		toolName:  "review",
		skillName: "review",
		runner:    nil, // no runner
	}
	// Any of the built-in skills won't resolve from the empty store, so this
	// hits the "skill not registered" path first. We test the runner=nil path
	// with an actual store containing the skill.
	home := t.TempDir()
	writeSkill(t, home, ".ok/skills/myexplore.md", "---\ndescription: me\nrunAs: subagent\n---\nb")
	st := New(Options{HomeDir: home, DisableBuiltins: true})
	tl2 := &subagentSkillTool{
		toolName:  "myexplore",
		skillName: "myexplore",
		store:     st,
		runner:    nil,
	}
	_, err := tl2.Execute(context.Background(), json.RawMessage(`{"task":"do it"}`))
	if err == nil || !strings.Contains(err.Error(), "runner") {
		t.Errorf("want 'runner' error, got %v", err)
	}
	_ = tl
}
