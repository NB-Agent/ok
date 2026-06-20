// Package boot initializes and assembles all components of the OK agent:
// config, providers, tools, plugins, permissions, hooks, memory, semantic search,
// and the controller. It is the composition root: every other package is wired
// together here so the frontends (CLI, HTTP, Desktop) only need one call to Build().
package boot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/bridge"
	"github.com/NB-Agent/ok/internal/brief"
	"github.com/NB-Agent/ok/internal/bus"
	"github.com/NB-Agent/ok/internal/codegraph"
	"github.com/NB-Agent/ok/internal/command"
	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/context7"
	"github.com/NB-Agent/ok/internal/control"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/dstsetup"
	"github.com/NB-Agent/ok/internal/dstvalid"
	"github.com/NB-Agent/ok/internal/env"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/eventpipe"
	"github.com/NB-Agent/ok/internal/evolution"
	"github.com/NB-Agent/ok/internal/hook"
	"github.com/NB-Agent/ok/internal/i18n"
	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/kernel"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/memory"
	"github.com/NB-Agent/ok/internal/metrics"
	"github.com/NB-Agent/ok/internal/permission"
	"github.com/NB-Agent/ok/internal/plugin"
	"github.com/NB-Agent/ok/internal/provider"
	_ "github.com/NB-Agent/ok/internal/provider/ollama" // register ollama kind; Available() checked below
	"github.com/NB-Agent/ok/internal/sandbox"
	"github.com/NB-Agent/ok/internal/semantic"
	"github.com/NB-Agent/ok/internal/skill"
	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/tool/builtin"
	"github.com/NB-Agent/ok/internal/voice"
	"github.com/NB-Agent/ok/internal/winhide"
)

// Options carries the per-run knobs a frontend chooses; everything else is read
// from configuration. Model "" falls back to the configured default_model;
// MaxSteps 0 uses the config/default. RequireKey forces the executor's API key to
// be present (run/serve pass true so a missing key fails fast; chat/desktop pass
// false so the UI is reachable before a key is set). Sink receives the agent's
// typed event stream.
type Options struct {
	Model      string
	MaxSteps   int
	RequireKey bool
	Sink       event.Sink
}

func Build(ctx context.Context, opts Options) (*control.Controller, error) {
	metrics.Start()
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	modelName := opts.Model
	if modelName == "" {
		modelName = cfg.DefaultModel
	}
	entry, ok := cfg.ResolveModel(modelName)
	if !ok {
		return nil, fmt.Errorf("unknown model %q (configured: %s)", modelName, providerNames(cfg))
	}
	if opts.RequireKey {
		if err := cfg.Validate(modelName); err != nil {
			return nil, err
		}
	}
	// Build typed event pipeline: frontend sink + JSONL log → Eventizer adapter.
	rawSink := opts.Sink
	if rawSink == nil {
		rawSink = event.Discard
	}
	var pipeSinks []eventpipe.Sink
	if sd := config.SessionDir(); sd != "" {
		if logSink, err := eventpipe.NewLogSink(eventpipe.LogConfig{
			Dir: sd, SessionID: "session",
		}); err == nil {
			pipeSinks = append(pipeSinks, logSink)
		}
	}
	// Bridge typed events to the old frontend sink so existing TUI/serve/desktop
	// code continues working without changes.
	if rawSink != nil {
		pipeSinks = append(pipeSinks, eventpipe.NewFrontendBridge(rawSink))
	}
	var pipeline eventpipe.Sink
	if len(pipeSinks) == 1 {
		pipeline = pipeSinks[0]
	} else {
		pipeline = eventpipe.FanOut(pipeSinks...)
	}
	ez := eventpipe.NewEventizer(pipeline)
	sink := event.Sync(ez)
	jm := jobs.NewManager(sink)
	execProv, err := NewProvider(entry)
	if err != nil {
		return nil, err
	}
	// Wire i18n translation resolver chain: compiled catalog → LiveResolver →
	// English fallback. This must happen after the provider is created but
	// before any agent turn that may display translated UI strings.
	lang := cfg.Language
	if lang == "" {
		lang = i18n.DetectLanguage("")
	}
	WireI18n(execProv, lang)
	// Note: Ollama fallback was removed because it caused connection errors when
	// Ollama is not running. Users who want a local fallback can configure Ollama
	// explicitly as a provider in ok.toml.

	mem := memory.Load(memory.Options{CWD: ".", UserDir: config.MemoryUserDir()})
	cwd, _ := os.Getwd()
	sysPrompt, envDiag, skillStore, err := assembleSystemPrompt(cfg, mem, cwd)
	if err != nil {
		return nil, err
	}
	skills := skillStore.List()
	reg := tool.NewRegistry()
	bashSpec := sandbox.Spec{Mode: cfg.BashMode(), WriteRoots: cfg.WriteRoots(), Network: cfg.Sandbox.Network}
	if bashSpec.Mode == "enforce" && !sandbox.Available() {
		msg := "bash sandbox requested but unavailable on this platform; running bash unconfined"
		if cfg.Sandbox.OnUnavailable == "block" {
			return nil, fmt.Errorf("sandbox: %s (set sandbox.on_unavailable = \"warn\" to override)", msg)
		}
		fmt.Fprintln(os.Stderr, "warning: "+msg)
	}
	if bashSpec.Mode == "appcontainer" && !sandbox.Available() {
		msg := "AppContainer sandbox requested but unavailable on Windows < 8; falling back to low-integrity sandbox"
		if cfg.Sandbox.OnUnavailable == "block" {
			return nil, fmt.Errorf("sandbox: %s (set sandbox.on_unavailable = \"warn\" to override)", msg)
		}
		fmt.Fprintln(os.Stderr, "warning: "+msg)
	}
	addBuiltins(reg, cfg.Tools.Enabled, cfg.WriteRoots(), cfg.ReadRoots(), bashSpec)
	pluginHost := plugin.NewHost()
	specs := PluginSpecs(cfg.Plugins)
	// v4: auto-detect MCP plugin binaries and replace builtin equivalents.
	// This reduces kernel tool schema from ~8000 to ~1500 tokens.
	autoSpecs, autoTools := detectV4Plugins(cwd)

	// Context7: auto-discover when CONTEXT7_API_KEY is set. Provides up-to-date
	// library documentation, eliminating API hallucinations from stale training data.
	if ctx7, ok7 := context7.Spec(); ok7 {
		autoSpecs = append(autoSpecs, ctx7)
	}

	if len(autoSpecs) > 0 {
		if !cfg.PluginQuiet {
			var names []string
			for _, s := range autoSpecs {
				names = append(names, s.Name)
			}
			sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo,
				Text: "auto-discovered " + fmt.Sprintf("%d plugin(s)", len(autoSpecs)) + ": " + strings.Join(names, ", ") +
					" — verify these are trusted before use"})
		}
		for _, tn := range autoTools {
			reg.RemovePrefix(tn)
		}
		specs = append(specs, autoSpecs...)
	}
	// Store auto-discovered plugin specs for health-check restart.
	autoSpecMap := map[string]plugin.Spec{}
	for _, s := range autoSpecs {
		autoSpecMap[s.Name] = s
	}
	if cfg.Codegraph.Enabled {
		if bin, ok := codegraph.Resolve(cfg.Codegraph.Path); ok {
			if err := codegraph.EnsureInit(ctx, bin, cwd); err != nil {
				sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
					Text: "codegraph: init failed (" + err.Error() + ") — running in degraded mode (no symbol index)"})
			} else {
				// Project brief (Stacklit): generate a concise overview that
				// gives the agent immediate context at session start.
				if err := brief.Generate(cwd); err != nil {
					sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
						Text: "brief: " + err.Error()})
				}
			}
			specs = append(specs, plugin.Spec{
				Name:    "codegraph",
				Command: bin,
				Args:    []string{"serve", "--mcp"},
				Dir:     cwd,
			})
		}
	}
	if len(specs) > 0 {
		host, ptools, err := plugin.StartAll(ctx, specs)
		if err != nil {
			return nil, fmt.Errorf("plugin: %w", err)
		}
		pluginHost = host
		for _, t := range ptools {
			reg.Add(t)
		}
	}
	cleanup := pluginHost.Close
	// Message bus for decoupled component communication.
	msgBus := bus.New()
	turnN := 0
	msgBus.Sub("turn:done", func(_ string, _ any) {
		turnN++
		if turnN%5 != 0 {
			return
		}
		hcCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		restarted := map[string]bool{}
		for _, r := range pluginHost.HealthCheck(hcCtx) {
			if !r.Alive {
				fmt.Fprintf(os.Stderr, "plugin health: %s is unhealthy: %s\n", r.Name, r.Err)
				if spec, ok := autoSpecMap[r.Name]; ok && !restarted[r.Name] {
					restarted[r.Name] = true
					prefix, found := pluginHost.Remove(r.Name)
					if found {
						reg.RemovePrefix(prefix)
					}
					tools, err := pluginHost.Add(hcCtx, spec)
					if err != nil {
						fmt.Fprintf(os.Stderr, "plugin health: %s restart failed: %v\n", r.Name, err)
					} else {
						fmt.Fprintf(os.Stderr, "plugin health: %s restarted successfully\n", r.Name)
						for _, t := range tools {
							reg.Add(t)
						}
					}
				}
			}
		}
	})
	maxSteps := cfg.Agent.MaxSteps
	if opts.MaxSteps > 0 {
		maxSteps = opts.MaxSteps
	}
	homeDir, _ := os.UserHomeDir()
	policyFile, policyErr := config.LoadPolicy(homeDir)
	if policyErr != nil {
		sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: "policy: " + policyErr.Error()})
	}
	mergedAllow, mergedAsk, mergedDeny := config.MergeModeRules(cfg.ModeAllow(), cfg.ModeAsk(), cfg.ModeDeny(), policyFile)
	policy := permission.NewPolicy(mergedAllow, mergedAsk, mergedDeny)
	headlessGate := permission.NewGate(policy, nil)
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
	hooksTrusted := hook.IsTrusted(cwd, "")
	hookRunner := hook.NewRunner(
		hook.Load(hook.LoadOptions{ProjectRoot: cwd, Trusted: hooksTrusted}),
		cwd, nil,
		func(msg string) { sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: msg}) },
	)
	if hook.ProjectDefinesHooks(cwd) && !hooksTrusted {
		sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelInfo,
			Text: "this project defines hooks but they are not trusted — run /hooks trust to enable them"})
	}
	proofChain := core.NewProofChain()
	auditChain := core.NewAuditChain()
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
	// Apply hierarchical tool groups so the model only sees core tools
	// by default, saving ~70% schema tokens per turn.
	for _, name := range reg.Names() {
		if g := builtin.ToolGroup(name); g != "" {
			reg.SetGroup(name, g)
		}
	}
	reg.ActivateGroups("core")
	semEngine := semantic.NewEngine(cwd)
	semEngine.BuildIndexAsync()
	builtin.SetSemanticEngine(semEngine)
	// Extend cleanup to shut down the background semantic indexer before exit.
	prevCleanup := cleanup
	cleanup = func() {
		prevCleanup()
		semEngine.Shutdown()
	}
	// Warn if the system prompt is approaching the context window — models
	// silently fail or produce garbled output when the prompt + conversation
	// exceeds their limit.
	const promptWarnFraction = 0.5
	if entry.ContextWindow > 0 && len(sysPrompt) > int(float64(entry.ContextWindow)*promptWarnFraction) {
		sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: fmt.Sprintf("system prompt is %d chars (%.0f%% of %d-token context window); consider trimming custom instructions",
				len(sysPrompt), float64(len(sysPrompt))/float64(entry.ContextWindow)*100, entry.ContextWindow)})
	}
	execSess := agent.NewSession(sysPrompt)
	executor := agent.New(execProv, reg, execSess, agent.Options{
		MaxSteps:      maxSteps,
		Temperature:   cfg.Agent.Temperature,
		Pricing:       entry.Price,
		Gate:          headlessGate,
		Hooks:         hookRunner,
		Jobs:          jm,
		ContextWindow: entry.ContextWindow,
		ArchiveDir:    config.ArchiveDir(),
		AuditChain:    auditChain,
	}, sink)
	executor.SetPipe(pipeline)

	// Wire episodic memory: save a summary to the memory store after each turn.
	// Significant turns (multi-tool, long output) also get saved to the shared
	// cross-project memory for knowledge transfer between projects.
	// The evolution engine auto-generates skill candidates from patterns.
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

	// Agent-to-agent P2P bridge: lets other OK instances discover and delegate tasks
	br := bridge.NewBridge(bridge.LoadOrCreateSecret(), 0, func(ctx context.Context, task string) (<-chan string, error) {
		ch := make(chan string, 1)
		go func() {
			defer close(ch)
			defer func() {
				if r := recover(); r != nil {
					log.Error("goroutine panic", "recover", r)
					ch <- fmt.Sprintf("bridge task panic: %v", r)
				}
			}()
			if err := executor.Run(ctx, task); err != nil {
				ch <- fmt.Sprintf("bridge task failed: %v", err)
				return
			}
			ch <- "bridge task completed"
		}()
		return ch, nil
	})
	if err := br.Start(); err != nil {
		sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: "bridge: P2P discovery not available: " + err.Error()})
	}
	prevCleanup3 := cleanup
	cleanup = func() {
		prevCleanup3()
		br.Stop()
	}

	var runner agent.Runner = executor
	compileCmd := cfg.Agent.CompileCmd
	if compileCmd == "" {
		compileCmd = "go build ./..."
	}
	testCmd := cfg.Agent.TestCmd
	if testCmd == "" {
		testCmd = "go test ./..."
	}
	dr := dstsetup.Init(executor, proofChain, cwd, compileCmd, testCmd)
	if dr != nil {
		dr.SetSink(sink)
		runner = dr
		if dr.Hooks() != nil {
			ah := dstvalid.NewAdvancedHooks(
				dr.Hooks(),
				cwd,
				compileCmd,
				testCmd,
				proofChain,
			)
			ah.SetLearnMode(true)
			ah.SetImpactMode(true)
			ah.SetTargetedTestMode(true)
			ah.SetCoverageMode(true)
			ah.SetCoverageThreshold(0.5)
			ah.SetStyleCheckMode(true)
			ah.SetOkVerifyMode(true)
			if mem.Store.Dir != "" {
				ah.SetLearningSaver(func(name, body string) {
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
			executor.SetHooks(ah)
		}
	}
	cmds, _ := command.Load(config.CommandDirs()...)
	label := entry.Model

	// resolveModel turns a model name into a provider+pricing+context-window
	// triple. Used by BuildTeam for orchestrator and specialists.
	resolveModel := func(modelName string) (provider.Provider, *provider.Pricing, int, error) {
		me, ok := cfg.ResolveModel(modelName)
		if !ok {
			return nil, nil, 0, fmt.Errorf("unknown model: %s", modelName)
		}
		p, err := NewProvider(me)
		if err != nil {
			return nil, nil, 0, err
		}
		return p, me.Price, me.ContextWindow, nil
	}

	// --- Team (multi-model agent team) ---
	if len(cfg.Team.Specialists) > 0 {
		teamCfg := agent.TeamConfig{
			Orchestrator: cfg.Team.Orchestrator,
		}
		for _, s := range cfg.Team.Specialists {
			teamCfg.Specialists = append(teamCfg.Specialists, agent.SpecialistConfig{
				Name:        s.Name,
				Model:       s.Model,
				Description: s.Description,
				Prompt:      s.Prompt,
				Tools:       s.Tools,
			})
		}
		team, err := agent.BuildTeam(teamCfg, resolveModel, sink, reg,
			headlessGate, hookRunner, agent.Options{
				MaxSteps:      maxSteps,
				Temperature:   cfg.Agent.Temperature,
				ContextWindow: entry.ContextWindow,
				ArchiveDir:    config.ArchiveDir(),
			})
		if err != nil {
			return nil, fmt.Errorf("team: %w", err)
		}
		runner = team
		label = "team: " + cfg.Team.Orchestrator
		for _, s := range cfg.Team.Specialists {
			label += " + " + s.Model
		}
		if cfg.Reasoner.DecomposeModel != "" {
			sink.Emit(&event.Event{Kind: event.Notice, Level: event.LevelWarn,
				Text: "reasoner ignored: [team] takes precedence"})
		}
	} else if cfg.Reasoner.DecomposeModel != "" {
		// --- Reasoner (DAG decomposition) ---
		re, ok := cfg.ResolveModel(cfg.Reasoner.DecomposeModel)
		if !ok {
			return nil, fmt.Errorf("reasoner.decompose_model %q is not configured", cfg.Reasoner.DecomposeModel)
		}
		reasonerProv, err := NewProvider(re)
		if err != nil {
			return nil, fmt.Errorf("reasoner: %w", err)
		}
		reasonerSess := agent.NewSession(agent.DefaultPlannerPrompt)
		maxConc := cfg.Reasoner.MaxConcurrent
		if maxConc <= 0 {
			maxConc = 3
		}
		dispatch := func(ctx context.Context, task agent.PlanTask) (string, error) {
			return agent.RunSubAgent(ctx, execProv, reg, sysPrompt, task.Description, agent.Options{
				MaxSteps:      maxSteps,
				Temperature:   cfg.Agent.Temperature,
				Pricing:       entry.Price,
				Gate:          headlessGate,
				Hooks:         hookRunner,
				Jobs:          jm,
				ContextWindow: entry.ContextWindow,
				ArchiveDir:    config.ArchiveDir(),
			}, agent.NestedSink(ctx, sink))
		}
		reasoner := agent.NewReasoner(reasonerProv, reasonerSess, re.Price, dispatch, maxConc, cfg.Agent.Temperature, sink)
		runner = reasoner
		label = "reasoner: " + cfg.Reasoner.DecomposeModel + " -> " + label
	} else if pm := cfg.Agent.PlannerModel; pm != "" {
		pe, ok := cfg.ResolveModel(pm)
		if !ok {
			return nil, fmt.Errorf("planner_model %q is not a configured provider", pm)
		}
		if pe.Model != entry.Model {
			plannerProv, err := NewProvider(pe)
			if err != nil {
				return nil, fmt.Errorf("planner %q: %w", pm, err)
			}
			plannerSess := agent.NewSession(agent.DefaultPlannerPrompt)
			if dr != nil {
				runner = agent.NewCoordinator(plannerProv, plannerSess, pe.Price, dr, entry.Model, cfg.Agent.Temperature, sink)
			} else {
				runner = agent.NewCoordinator(plannerProv, plannerSess, pe.Price, executor, entry.Model, cfg.Agent.Temperature, sink)
			}
			label = entry.Model + " + planner " + pe.Model
		}
	}
	kern := &kernel.Kernel{
		Sandbox:    kernel.NewSandbox(bashSpec, cwd),
		Session:    kernel.NewSession(execSess),
		Provider:   kernel.NewProvider(execProv),
		Controller: nil, // wired after ctrl is created below
		Bash:       kernel.NewBash(kernel.NewSandbox(bashSpec, cwd)),
		ReadFile:   kernel.NewReadFile(cfg.ReadRoots(), cwd),
		WriteFile:  kernel.NewWriteFile(cfg.WriteRoots(), cwd),
		EditFile:   kernel.NewEditFile(cfg.WriteRoots(), cwd),
		Grep:       kernel.NewGrep(cfg.ReadRoots(), cwd),
		Identity:   kernel.NewIdentity(),
		Recall:     kernel.NewRecallWithSemantic(&mem.Store, kernel.NewSemanticEngineAdapter(semEngine)),
		Trust:      kernel.NewTrust(proofChain, auditChain),
		Learn:      evol, // evolution.Engine implements kernel.Learn (dual-path unification)
	}
	// Register civilization primitives as LLM-accessible tools.
	reg.Add(kernel.NewIdentityTool(kern.Identity))
	reg.Add(kernel.NewRecallTool(kern.Recall))
	reg.Add(kernel.NewTrustTool(kern.Trust))
	reg.Add(kernel.NewLearnTool(kern.Learn))

	ctrl := control.New(control.Options{
		Runner:       runner,
		Executor:     executor,
		Sink:         sink,
		Policy:       policy,
		MsgBus:       msgBus,
		Label:        label,
		SystemPrompt: sysPrompt,
		SessionDir:   config.SessionDir(),
		WorkDir:      cwd,
		Host:         pluginHost,
		Commands:     cmds,
		Skills:       skills,
		Hooks:        hookRunner,
		Memory:       mem,
		Cleanup:      cleanup,
		BalanceURL:   entry.BalanceURL,
		BalanceKey:   entry.APIKey(),
		Jobs:         jm,
		Registry:     reg,
		PluginCtx:    ctx,
		ProofChain:   proofChain,
		AuditChain:   auditChain,
		OnRemember:   headlessGate.OnRemember,
		EnvDiagnosis: envDiag,
		Kernel:       kern,
		EvolEngine:   evol,
		EvolSecret:   cfg.ECP.SharedSecret,
	})
	// Wire the kernel's Controller primitive so frontends can read it.
	kern.Controller = newKernelControllerAdapter(ctrl)
	if cfg.Mode.Default == "yolo" {
		ctrl.SetBypass(true)
	} else if cfg.Mode.Default == "plan" && !opts.RequireKey {
		ctrl.SetPlanMode(true)
	}
	return ctrl, nil
}

func NewProvider(e *config.ProviderEntry) (provider.Provider, error) {
	return provider.New(e.Kind, provider.Config{
		Name:      e.Name,
		BaseURL:   e.BaseURL,
		Model:     e.Model,
		APIKey:    e.APIKey(),
		APIKeyEnv: e.APIKeyEnv,
	})
}

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

// v4PluginMapping maps MCP plugin binary names to the builtin tools they replace.
// When a plugin binary is found in plugins/, its tools replace the builtin equivalents,
// reducing kernel schema tokens by ~90%.
// pluginManifest is read from plugin.json in each plugin directory.
type pluginManifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Entrypoint  string   `json:"entrypoint"`
	Tools       []string `json:"tools"`
	Transport   string   `json:"transport"`
	Description string   `json:"description"`
	MinOKVer    string   `json:"min_ok_version"`
}

// detectV4Plugins scans the plugins/ directory for MCP plugin binaries by
// reading each subdirectory's plugin.json. This is fully dynamic — adding a new
// plugin means dropping its directory under plugins/; no code change needed.
func detectV4Plugins(cwd string) ([]plugin.Spec, []string) {
	pluginDir := filepath.Join(cwd, "plugins")
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return nil, nil
	}
	var specs []plugin.Spec
	var toolNames []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pj := filepath.Join(pluginDir, e.Name(), "plugin.json")
		data, err := os.ReadFile(pj)
		if err != nil {
			continue
		}
		var m pluginManifest
		if json.Unmarshal(data, &m) != nil || m.Entrypoint == "" || len(m.Tools) == 0 {
			continue
		}
		winPath := filepath.Join(pluginDir, e.Name(), m.Entrypoint+".exe")
		unixPath := filepath.Join(pluginDir, e.Name(), m.Entrypoint)
		var found string
		if _, err := os.Stat(winPath); err == nil {
			found = winPath
		} else if _, err := os.Stat(unixPath); err == nil {
			found = unixPath
		}
		if found == "" {
			continue
		}
		pluginName := e.Name()
		if m.Name != "" {
			pluginName = m.Name
		}
		specs = append(specs, plugin.Spec{
			Name:    pluginName,
			Command: found,
			Dir:     cwd,
		})
		toolNames = append(toolNames, m.Tools...)
	}
	return specs, toolNames
}

func PluginSpecs(entries []config.PluginEntry) []plugin.Spec {
	specs := make([]plugin.Spec, len(entries))
	for i, e := range entries {
		e = e.ExpandedPlugin()
		specs[i] = plugin.Spec{
			Name: e.Name, Type: e.Type, Command: e.Command,
			Args: e.Args, Env: e.Env, URL: e.URL, Headers: e.Headers,
		}
	}
	return specs
}

func providerNames(cfg *config.Config) string {
	names := make([]string, len(cfg.Providers))
	for i, p := range cfg.Providers {
		names[i] = p.Name
	}
	return strings.Join(names, "/")
}

type proofChainAdapter struct{ pc *core.ProofChain }

func (a *proofChainAdapter) AppendWithPath(atomID, proposition, evidence, parentID, path string) {
	a.pc.AppendWithPath(atomID, proposition, evidence, parentID, path)
}

// assembleSystemPrompt composes the cache-stable system prompt from raw config,
// language policy, memory, shared knowledge, and skill index. Returns the
// assembled prompt and the env+boot diagnosis (for turn-level injection, so the
// system-prompt prefix stays byte-identical across turns).
func assembleSystemPrompt(cfg *config.Config, mem *memory.Set, cwd string) (prompt, envDiag string, store *skill.Store, err error) {
	raw, err := cfg.ResolveSystemPrompt()
	if err != nil {
		return "", "", nil, err
	}
	prompt = raw + "\n\n" + config.LanguagePolicy
	prompt = memory.Compose(prompt, mem)
	if sharedIdx := memory.SharedStoreFor(config.MemoryUserDir()).Index(); sharedIdx != "" {
		prompt = prompt + "\n\n# Shared Knowledge\n\n" + sharedIdx
	}
	store = skill.New(skill.Options{ProjectRoot: cwd, CustomPaths: cfg.SkillCustomPaths()})
	prompt = skill.ApplyIndex(prompt, store.List())
	envDiag = env.Context(cfg)
	if d := bootDiagnose(context.Background(), cwd, *mem); d != "" {
		envDiag = d + "\n" + envDiag
	}
	return prompt, envDiag, store, nil
}

func bootDiagnose(ctx context.Context, cwd string, mem memory.Set) string {
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
	if idx := mem.Index; idx != "" {
		lines := strings.Count(idx, "\n") + 1
		if lines > 0 {
			b.WriteString(fmt.Sprintf("- Memory entries: %d\n", lines))
		}
	}
	if b.Len() > 0 {
		return b.String()
	}
	return ""
}

// ─── kernelControllerAdapter ─────────────────────────────────────────────

// kernelControllerAdapter wraps *control.Controller to implement kernel.Controller.
// Defined here (not in kernel/) to avoid a circular import: kernel cannot import control.
type kernelControllerAdapter struct {
	ctrl *control.Controller
}

func newKernelControllerAdapter(ctrl *control.Controller) kernel.Controller {
	return &kernelControllerAdapter{ctrl: ctrl}
}

func (a *kernelControllerAdapter) Send(ctx context.Context, input string) error {
	a.ctrl.Send(input)
	return nil
}

func (a *kernelControllerAdapter) Cancel() { a.ctrl.Cancel() }

func (a *kernelControllerAdapter) SetPlanMode(v bool) { a.ctrl.SetPlanMode(v) }

func (a *kernelControllerAdapter) Events() <-chan kernel.Event {
	// The kernel Event channel is a secondary observation path. The primary
	// event flow goes through the typed event.Sink. Return a closed channel
	// so callers that range over it exit immediately rather than blocking
	// forever on nil.
	ch := make(chan kernel.Event)
	close(ch)
	return ch
}
