// Package boot initializes and assembles all components of the OK agent:
// config, providers, tools, plugins, permissions, hooks, memory, semantic search,
// and the controller. It is the composition root: every other package is wired
// together here so the frontends (CLI, HTTP, Desktop) only need one call to Build().
package boot

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/bridge"
	"github.com/NB-Agent/ok/internal/bus"
	"github.com/NB-Agent/ok/internal/command"
	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/control"
	"github.com/NB-Agent/ok/internal/core"
	"github.com/NB-Agent/ok/internal/dstsetup"
	"github.com/NB-Agent/ok/internal/dstvalid"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/eventpipe"
	"github.com/NB-Agent/ok/internal/hook"
	"github.com/NB-Agent/ok/internal/i18n"
	"github.com/NB-Agent/ok/internal/jobs"
	"github.com/NB-Agent/ok/internal/kernel"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/memory"
	"github.com/NB-Agent/ok/internal/metrics"
	"github.com/NB-Agent/ok/internal/permission"
	"github.com/NB-Agent/ok/internal/provider"
	_ "github.com/NB-Agent/ok/internal/provider/ollama" // register ollama kind; Available() checked below
	"github.com/NB-Agent/ok/internal/semantic"
	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/tool/builtin"
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
	sysPrompt, firstTurnInject, envDiag, skillStore, err := assembleSystemPrompt(cfg, mem, cwd)
	if err != nil {
		return nil, err
	}
	skills := skillStore.List()
	reg := tool.NewRegistry()
	bashSpec, err := setupSandboxAndBuiltins(reg, cfg)
	if err != nil {
		return nil, err
	}
	pluginHost, cleanup, autoSpecMap, err := loadPlugins(ctx, cfg, reg, cwd, sink)
	if err != nil {
		return nil, err
	}
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
	setupOnRemember(cfg, headlessGate, sink)
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
	registerAgentTools(reg, execProv, entry, maxSteps, headlessGate, cwd, proofChain, hookRunner, jm, cfg, mem, skillStore, sink)
	// Apply hierarchical tool groups so the model only sees core tools
	// by default, saving ~70% schema tokens per turn.
	applyToolGroups(reg)
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
	evol := setupEvolutionAndEpisodic(mem, executor, sink, cwd, cfg, skillStore)

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
			setupLearningSaver(mem, sink, ah)
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
	registerKernelTools(reg, kern)

	ctrl := control.New(control.Options{
		Runner:          runner,
		Executor:        executor,
		Sink:            sink,
		Policy:          policy,
		MsgBus:          msgBus,
		Label:           label,
		SystemPrompt:    sysPrompt,
		SessionDir:      config.SessionDir(),
		WorkDir:         cwd,
		Host:            pluginHost,
		Commands:        cmds,
		Skills:          skills,
		Hooks:           hookRunner,
		Memory:          mem,
		Cleanup:         cleanup,
		BalanceURL:      entry.BalanceURL,
		BalanceKey:      entry.APIKey(),
		Jobs:            jm,
		Registry:        reg,
		PluginCtx:       ctx,
		ProofChain:      proofChain,
		AuditChain:      auditChain,
		OnRemember:      headlessGate.OnRemember,
		EnvDiagnosis:    envDiag,
		FirstTurnInject: firstTurnInject,
		Kernel:          kern,
		EvolEngine:      evol,
		EvolSecret:      cfg.ECP.SharedSecret,
	})
	// Wire the kernel's Controller primitive so frontends can read it.
	kern.Controller = newKernelControllerAdapter(ctrl)
	ctrl.SetBypass(true)
	return ctrl, nil
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
