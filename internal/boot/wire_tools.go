package boot

import (
	"context"
	"fmt"
	"os"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/dstvalid"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/hook"
	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/kernel"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/memory"
	"github.com/NB-Agent/ok/internal/permission"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/sandbox"
	"github.com/NB-Agent/ok/internal/skill"
	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/tool/builtin"
	"github.com/NB-Agent/ok/internal/voice"
)

// addBuiltins registers the standard built-in tools (bash, read, write, edit,
// grep, glob, etc.) respecting the enabled allowlist and applying sandbox
// confinement for writers, readers, and bash.
func addBuiltins(reg *tool.Registry, enabled, writeRoots, readRoots []string, bashSpec sandbox.Spec) {
	if len(enabled) == 0 {
		for _, t := range tool.Builtins() {
			reg.Add(t)
		}
	} else {
		for _, name := range enabled {
			if t, ok := tool.LookupBuiltin(name); ok {
				reg.Add(t)
			} else {
				log.Warn("boot: unknown built-in tool", "name", name)
			}
		}
	}
	confined := append(builtin.ConfineWriters(writeRoots),
		append(builtin.ConfineReaders(readRoots), builtin.ConfineBash(bashSpec))...)
	for _, t := range confined {
		if _, ok := reg.Get(t.Name()); ok {
			reg.Add(t)
		}
	}
}

// setupSandboxAndBuiltins validates the sandbox configuration and registers
// all built-in tools. Returns the resolved bash sandbox spec.
func setupSandboxAndBuiltins(reg *tool.Registry, cfg *config.Config) (sandbox.Spec, error) {
	bashSpec := sandbox.Spec{Mode: cfg.BashMode(), WriteRoots: cfg.WriteRoots(), Network: cfg.Sandbox.Network}
	if bashSpec.Mode == "enforce" && !sandbox.Available() {
		msg := "bash sandbox requested but unavailable on this platform; running bash unconfined"
		if cfg.Sandbox.OnUnavailable == "block" {
			return sandbox.Spec{}, fmt.Errorf("sandbox: %s (set sandbox.on_unavailable = \"warn\" to override)", msg)
		}
		fmt.Fprintln(os.Stderr, "warning: "+msg)
	}
	if bashSpec.Mode == "appcontainer" && !sandbox.Available() {
		msg := "AppContainer sandbox requested but unavailable on Windows < 8; falling back to low-integrity sandbox"
		if cfg.Sandbox.OnUnavailable == "block" {
			return sandbox.Spec{}, fmt.Errorf("sandbox: %s (set sandbox.on_unavailable = \"warn\" to override)", msg)
		}
		fmt.Fprintln(os.Stderr, "warning: "+msg)
	}
	addBuiltins(reg, cfg.Tools.Enabled, cfg.WriteRoots(), cfg.ReadRoots(), bashSpec)
	return bashSpec, nil
}

// registerAgentTools adds high-level agent tools: task, remember, ask,
// computer-use, translate, ocr, tool-groups, voice, skill, and agent-def tools.
func registerAgentTools(reg *tool.Registry, execProv provider.Provider, entry *config.ProviderEntry, maxSteps int, headlessGate *permission.Gate, cwd string, proofChain *core.ProofChain, hookRunner *hook.Runner, jm *jobs.Manager, cfg *config.Config, mem *memory.Set, skillStore *skill.Store, sink event.Sink) {
	var taskPricing *provider.Pricing
	if entry.Price != nil {
		taskPricing = entry.Price
	}
	reg.Add(agent.NewTaskToolFull(execProv, taskPricing, reg, maxSteps,
		entry.ContextWindow, cfg.Agent.Temperature, config.ArchiveDir(), "", headlessGate, cwd,
		func() agent.Snapshotter { return dstvalid.NewSnapshot() },
		&proofChainAdapter{proofChain},
		cfg.Agent.MaxConcurrentTasks,
		hookRunner, jm,
		config.LanguagePolicy))
	reg.Add(memory.NewRememberTool(mem.Store))
	reg.Add(agent.NewAskTool())
	cu := builtin.NewComputerUseTool(entry.BaseURL, entry.APIKey(), entry.DefaultModel())
	reg.Add(cu)
	reg.Add(builtin.NewTranslateTool(entry.BaseURL, entry.APIKey(), entry.DefaultModel()))
	reg.Add(builtin.NewOCRTool(entry.BaseURL, entry.APIKey(), entry.DefaultModel()))
	reg.Add(builtin.NewToolGroupsTool(reg))
	// Default to core tool group only (~15 tools, ~1500 tokens) instead of
	// all 50+ tools (~5000 tokens). The agent can activate advanced/knowledge/
	// admin groups via the tool-groups tool when needed.
	reg.ActivateGroups("core")
	// Voice interaction: STT/TTS via Whisper.cpp + Piper
	reg.Add(&voice.Tool{Engine: voice.NewEngine(cfg.Language)})
	skillRunner := func(sctx context.Context, sk skill.Skill, task string) (string, error) {
		prov, price, ctxWin := execProv, entry.Price, entry.ContextWindow
		if sk.Model != "" {
			if me, ok := cfg.ResolveModel(sk.Model); ok {
				if p, err := NewProvider(me); err == nil {
					prov, price, ctxWin = p, me.Price, me.ContextWindow
				}
			}
		}
		subReg := agent.FilterRegistry(reg, sk.AllowedTools,
			"task", "run_skill", "install_skill", "review", "security_review", "computer-use", "tool-groups")
		steps := maxSteps
		if steps > 0 {
			if steps /= 2; steps < 3 {
				steps = 3
			}
		}
		sysPrompt := sk.Body
		if config.LanguagePolicy != "" {
			sysPrompt += "\n\n" + config.LanguagePolicy
		}
		return agent.RunSubAgent(sctx, prov, subReg, sysPrompt, task, agent.Options{
			MaxSteps:      steps,
			Temperature:   cfg.Agent.Temperature,
			Pricing:       price,
			Gate:          headlessGate,
			Hooks:         hookRunner,
			Jobs:          jm,
			ContextWindow: ctxWin,
			ArchiveDir:    config.ArchiveDir(),
		}, agent.NestedSink(sctx, event.Discard))
	}
	reg.Add(skill.NewRunSkillTool(skillStore, skillRunner))
	reg.Add(skill.NewInstallSkillTool(skillStore, nil))
	for _, t := range skill.BuiltinSubagentTools(skillStore, skillRunner) {
		reg.Add(t)
	}
	for _, def := range agent.LoadAgentDefs(cwd) {
		reg.Add(def.MakeAgentTool(execProv, reg, entry.Price, headlessGate,
			hookRunner, jm, agent.Options{
				MaxSteps:      maxSteps,
				Temperature:   cfg.Agent.Temperature,
				ContextWindow: entry.ContextWindow,
				ArchiveDir:    config.ArchiveDir(),
			},
			func(modelName string) (provider.Provider, *provider.Pricing, int, error) {
				me, ok := cfg.ResolveModel(modelName)
				if !ok {
					return nil, nil, 0, fmt.Errorf("unknown model: %s", modelName)
				}
				p, err := NewProvider(me)
				if err != nil {
					return nil, nil, 0, err
				}
				return p, me.Price, me.ContextWindow, nil
			}))
	}
}

// applyToolGroups assigns hierarchical tool groups so the model only sees
// core tools by default, saving ~70% schema tokens per turn.
func applyToolGroups(reg *tool.Registry) {
	for _, name := range reg.Names() {
		if g := builtin.ToolGroup(name); g != "" {
			reg.SetGroup(name, g)
		}
	}
	reg.ActivateGroups("core")
}

// registerKernelTools adds civilization primitives as LLM-accessible tools.
func registerKernelTools(reg *tool.Registry, kern *kernel.Kernel) {
	reg.Add(kernel.NewIdentityTool(kern.Identity))
	reg.Add(kernel.NewRecallTool(kern.Recall))
	reg.Add(kernel.NewTrustTool(kern.Trust))
	reg.Add(kernel.NewLearnTool(kern.Learn))
}
