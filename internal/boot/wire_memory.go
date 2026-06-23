package boot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/dstvalid"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/evolution"
	"github.com/NB-Agent/ok/internal/memory"
	"github.com/NB-Agent/ok/internal/permission"
	"github.com/NB-Agent/ok/internal/skill"
)

// isQualityMemory filters out low-value auto-learned entries (e.g. "edited
// X via Y — OK"). A memory must be long enough AND contain at least one signal
// word that indicates an actual lesson or reusable rule.
func isQualityMemory(body string) bool {
	if len(body) < 80 {
		return false
	}
	lower := strings.ToLower(body)
	signals := []string{"because", "pattern", "convention", "prefer",
		"avoid", "never", "always", "rule", "lesson", "should", "must not"}
	for _, s := range signals {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// setupOnRemember wires the OnRemember callback on the headless gate,
// persisting allow rules to ok.toml and emitting notice events.
func setupOnRemember(cfg *config.Config, headlessGate *permission.Gate, sink event.Sink) {
	headlessGate.OnRemember = func(rule string) {
		if err := cfg.AddPermissionRule("allow", rule); err != nil {
			sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
				Text: "failed to persist allow rule: " + err.Error()})
			return
		}
		if err := cfg.Save(); err != nil {
			sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
				Text: "failed to save config: " + err.Error()})
			return
		}
		sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo,
			Text: fmt.Sprintf("persisted allow rule to ok.toml: %s", rule)})
	}
}

// setupEvolutionAndEpisodic wires the evolution engine, ECP federation (if
// enabled), and episodic memory saving after each turn. Returns the evolution
// engine so the caller can attach it to the kernel.
func setupEvolutionAndEpisodic(mem *memory.Set, executor *agent.Agent, sink event.Sink, cwd string, cfg *config.Config, skillStore *skill.Store) *evolution.Engine {
	evol := evolution.New(mem, skillStore, filepath.Join(mem.CWD, ".ok", "memory"))

	// ECP Federation — when enabled, periodically sync learned skills with peers.
	if cfg.ECP.Enabled && len(cfg.ECP.Peers) > 0 {
		interval := time.Hour
		if d, err := time.ParseDuration(cfg.ECP.SyncInterval); err == nil && d > 0 {
			interval = d
		}
		transport := evolution.NewHTTPPeer(30 * time.Second)
		transport.SharedSecret = cfg.ECP.SharedSecret
		fed := evolution.NewFederator(evolution.FederatorConfig{
			Transport:  transport,
			Peers:      cfg.ECP.Peers,
			Interval:   interval,
			InstanceID: "ok-" + os.Getenv("COMPUTERNAME"),
		})
		fed.Start()
	}
	if mem.Store.Dir != "" {
		var episodicCounter atomic.Int32
		executor.SetOnTurnComplete(func(ctx context.Context, input, answer string) {
			// Self-evolution: auto-extract experiences and detect patterns
			evol.OnTurnComplete(ctx, input, answer)
			// Determine significance: did this turn produce substantial output?
			significant := len(answer) > 2000 // long output = complex task

			desc := input
			if len(desc) > 100 {
				desc = desc[:100] + "..."
			}
			body := answer
			if len(body) > 200 {
				body = body[:200] + "..."
			}

			// Batch episodic saves: only every 5 turns, so the memory
			// index churns less between sessions and the cache-stable system-prompt
			// prefix stays byte-identical for longer.
			sn := episodicCounter.Add(1)
			if sn%5 == 1 {
				_, err := mem.Store.Save(memory.Memory{
					Name:        "episodic-" + time.Now().Format("20060102-150405"),
					Description: desc,
					Type:        memory.TypeProject,
					Body: fmt.Sprintf("## Input\n%s\n\n## Outcome\n%s\n\n---\n*Auto-saved episodic memory*",
						desc, body),
				})
				if err != nil {
					sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
						Text: "failed to save episodic memory: " + err.Error()})
				}
			}

			// Significant turns also get saved to shared memory for cross-project learning.
			if significant {
				shared := memory.SharedStoreFor(config.MemoryUserDir())
				if shared.Dir != "" {
					if _, err := shared.Save(memory.Memory{
						Name:        "shared-" + time.Now().Format("20060102-150405"),
						Description: "[shared] " + desc,
						Type:        memory.TypeFeedback,
						Body: fmt.Sprintf("## Cross-project insight from %s\n\n### Input\n%s\n\n### Outcome\n%s\n\n---\n*Auto-saved shared memory*",
							cwd, desc, body),
					}); err != nil {
						fmt.Fprintf(os.Stderr, "boot: save shared memory: %v\n", err)
					}
				}
			}
		})
	}
	return evol
}

// setupLearningSaver wires the advanced hooks to auto-save quality memories
// after each successful edit (compile+test pass).
func setupLearningSaver(mem *memory.Set, sink event.Sink, ah *dstvalid.AdvancedHooks) {
	if mem.Store.Dir == "" {
		return
	}
	ah.SetLearningSaver(func(name, body string) {
		if !isQualityMemory(body) {
			return
		}
		_, err := mem.Store.Save(memory.Memory{
			Name:        name,
			Description: "auto-learned edit",
			Type:        memory.TypeFeedback,
			Body:        fmt.Sprintf("## 情境\n编辑了项目文件。\n\n## 行动\n%s\n\n## 结果\n编译+测试通过", body),
		})
		if err != nil {
			sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
				Text: "failed to save auto-learned memory: " + err.Error()})
		}
	})
}
