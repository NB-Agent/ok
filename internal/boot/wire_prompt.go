package boot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/env"
	"github.com/NB-Agent/ok/internal/memory"
	"github.com/NB-Agent/ok/internal/skill"
	"github.com/NB-Agent/ok/internal/winhide"
)

// assembleSystemPrompt splits the prompt into a cache-stable system prefix and
// a firstTurnInject block. Only the base prompt + language policy stay in the
// system message — memory, shared knowledge, and skill index ride the first
// user turn via Compose so the system-prompt prefix stays byte-identical
// across sessions (warming DeepSeek's prefix cache).
func assembleSystemPrompt(cfg *config.Config, mem *memory.Set, cwd string) (prompt, firstTurnInject, envDiag string, store *skill.Store, err error) {
	raw, err := cfg.ResolveSystemPrompt()
	if err != nil {
		return "", "", "", nil, err
	}
	prompt = raw + "\n\n" + config.LanguagePolicy

	// First-turn inject: memory block, shared knowledge, skill index.
	// These ride the first user turn via Compose so the system prompt stays
	// byte-stable across sessions (warming DeepSeek's prefix cache).
	firstTurnInject = mem.Block()
	sharedStore := memory.SharedStoreFor(config.MemoryUserDir())
	if sharedIdx := sharedStore.Index(); sharedIdx != "" {
		// Truncate to the first 30 entries so the shared section never bloats
		// the cache-stable system-prompt prefix. Full index is available via
		// recall/rag.
		lines := strings.Split(sharedIdx, "\n")
		if len(lines) > 30 {
			sharedIdx = strings.Join(lines[:30], "\n") +
				"\n\n… and more (use `recall` (core) or activate knowledge group for `rag`/`semantic-search` to search all memories)\n"
		}
		firstTurnInject = firstTurnInject + "\n\n# Shared Knowledge\n\n" + sharedIdx
	}
	store = skill.New(skill.Options{ProjectRoot: cwd, CustomPaths: cfg.SkillCustomPaths()})
	firstTurnInject = skill.ApplyIndex(firstTurnInject, store.List())
	envDiag = env.Context(cfg)
	if d := bootDiagnose(context.Background(), cwd); d != "" {
		envDiag = d + "\n" + envDiag
	}
	return prompt, firstTurnInject, envDiag, store, nil
}

func bootDiagnose(ctx context.Context, cwd string) string {
	if cwd == "" {
		return ""
	}
	var b strings.Builder
	gitCtx, gitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer gitCancel()
	// Single git call: --porcelain -b gives branch + dirty status.
	gitOut, err := winhide.CommandContext(gitCtx, "git", "status", "--porcelain", "-b").CombinedOutput()
	if err == nil {
		lines := strings.Split(string(gitOut), "\n")
		branch := ""
		dirty := ""
		for i, line := range lines {
			if i == 0 && strings.HasPrefix(line, "## ") {
				// "## main...origin/main" or "## HEAD (no branch)"
				branch = strings.TrimPrefix(line, "## ")
				if idx := strings.Index(branch, "..."); idx > 0 {
					branch = branch[:idx]
				}
			} else if strings.TrimSpace(line) != "" {
				dirty = " 📝dirty"
			}
		}
		if branch == "" {
			branch = "unknown"
		}
		b.WriteString(fmt.Sprintf("# Self state\n- Git: %s%s\n", branch, dirty))
	}
	if entries, err := os.ReadDir(filepath.Join(cwd, ".ok", "skills")); err == nil {
		count := 0
		for _, e := range entries {
			if !e.IsDir() {
				count++
			}
		}
		b.WriteString(fmt.Sprintf("- Skills: %d\n", count))
	}
	if entries, err := os.ReadDir(filepath.Join(cwd, "internal", "tool", "builtin")); err == nil {
		toolCount := 0
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				toolCount++
			}
		}
		b.WriteString(fmt.Sprintf("- Built-in tools: %d\n", toolCount))
	}
	if b.Len() > 0 {
		return b.String()
	}
	return ""
}
