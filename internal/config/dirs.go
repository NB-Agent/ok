package config

import (
	"os"
	"path/filepath"
)

// userOKDir joins sub-path elements under the ok user config directory
// (…/ok). Returns "" when the user config dir can't be resolved.
func userOKDir(sub ...string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(append([]string{dir, "ok"}, sub...)...)
}

// ArchiveDir is where compacted conversation history is archived for
// traceability (one timestamped .jsonl per compaction). Empty if the user config
// directory cannot be resolved, in which case archiving is skipped.
func ArchiveDir() string {
	return userOKDir("archive")
}

// SessionDir is where chat sessions are persisted (one .jsonl per session).
// Used by `ok chat --continue` / `--resume` to find the recent ones. Empty
// if the user config dir can't be resolved — sessions then aren't saved.
func SessionDir() string {
	return userOKDir("sessions")
}

// MemoryUserDir returns the ok user config root (…/ok), under which
// the user-global OK.md and the per-project auto-memory store live. Empty
// when the user config dir can't be resolved, which disables user-scoped memory.
func MemoryUserDir() string {
	return userOKDir()
}

// conventionDirs are the parent directories scanned for agent assets (skills,
// commands), in canonical-first order. .ok is ours; .agents / .agent /
// .claude let users drop in assets authored for other agent tools without moving
// files. Shared so skills (internal/skill) and commands (CommandDirs) discover
// the same set. Note: hooks are NOT scanned across these — a .claude/settings.json
// uses a different hook schema that can't be parsed as ours, so hooks stay in
// .ok/settings.json (see internal/hook).
var conventionDirs = [4]string{".ok", ".agents", ".agent", ".claude"}

// ConventionDirs returns a copy of the asset scan directories.
func ConventionDirs() []string {
	return []string{".ok", ".agents", ".agent", ".claude"}
}

// conventionSubdirsAsc joins sub under each ConventionDir of base, in ascending
// priority (reverse of ConventionDirs) so the canonical .ok ends up the
// highest-priority entry — command.Load lets a later directory win on a clash.
func conventionSubdirsAsc(base, sub string) []string {
	out := make([]string, 0, len(conventionDirs))
	for i := len(conventionDirs) - 1; i >= 0; i-- {
		out = append(out, filepath.Join(base, conventionDirs[i], sub))
	}
	return out
}

// CommandDirs returns the directories scanned for custom slash commands, lowest
// priority first, so a later (more specific) directory overrides an earlier one
// on a name clash. Order: home-dir convention dirs (~/.claude/commands … ~/.ok/commands),
// the legacy XDG user dir (~/.config/ok/commands), then the project's
// convention dirs (.claude/commands … .ok/commands). Scanning the .claude /
// .agents / .agent dirs lets commands authored for other agent tools (same .md +
// frontmatter format) work here unchanged.
func CommandDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, conventionSubdirsAsc(home, "commands")...)
	}
	if dir, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(dir, "ok", "commands")) // legacy XDG user dir
	}
	dirs = append(dirs, conventionSubdirsAsc(".", "commands")...)
	return dirs
}

// SourcePath returns the highest-priority config file that exists, or "" if none.
func SourcePath() string {
	if _, err := os.Stat("ok.toml"); err == nil {
		return "ok.toml"
	}
	if uc := userConfigPath(); uc != "" {
		if _, err := os.Stat(uc); err == nil {
			return uc
		}
	}
	return ""
}
