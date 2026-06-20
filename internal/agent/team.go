package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/provider"
	"github.com/NB-Agent/ok/internal/tool"
)

// Specialist describes one agent in a team.
type Specialist struct {
	Name          string
	Description   string
	Model         string
	Prov          provider.Provider
	Pricing       *provider.Pricing
	ContextWindow int
	Tools         []string       // tool names for display / filtering
	Reg           *tool.Registry // pre-filtered registry; nil means empty
	Prompt        string
}

// Team runs a multi-model agent team with an orchestrator + specialists.
type Team struct {
	Orchestrator Runner
	specialists  map[string]*SpecialistRunner
	tools        *tool.Registry
	sink         event.Sink
}

// SpecialistRunner wraps a specialist with its own registry and session.
type SpecialistRunner struct {
	Specialist
	reg  *tool.Registry
	sess *Session
}

// NewTeam builds a Team. The orchestrator gets a delegate tool to call specialists.
func NewTeam(orchestrator Runner, specialists []Specialist, sink event.Sink) *Team {
	specMap := make(map[string]*SpecialistRunner, len(specialists))
	reg := tool.NewRegistry()
	for _, s := range specialists {
		sess := NewSession(s.Prompt)
		srreg := s.Reg
		if srreg == nil {
			srreg = tool.NewRegistry()
		}
		sr := &SpecialistRunner{
			Specialist: s,
			reg:        srreg,
			sess:       sess,
		}
		specMap[s.Name] = sr
		reg.Add(&delegateTool{name: s.Name, description: s.Description, team: nil})
	}

	t := &Team{
		Orchestrator: orchestrator,
		specialists:  specMap,
		tools:        reg,
		sink:         sink,
	}
	for _, name := range reg.Names() {
		if tl, ok := reg.Get(name); ok {
			if d, ok := tl.(*delegateTool); ok {
				d.team = t
			}
		}
	}
	return t
}

func (t *Team) Run(ctx context.Context, input string) error {
	t.sink.Emit(&event.Event{Kind: event.TurnStarted})
	t.sink.Emit(&event.Event{Kind: event.Phase, Text: "team orchestrating"})
	if t.Orchestrator == nil {
		return fmt.Errorf("team: no orchestrator set")
	}
	return t.Orchestrator.Run(ctx, input)
}

// RunParallel dispatches a task to multiple specialists concurrently and
// aggregates their results. All specialists receive the same task; the
// orchestrator receives each result labeled by specialist name.
func (t *Team) RunParallel(ctx context.Context, task string, names []string) (map[string]string, error) {
	if len(names) == 0 {
		// Default: run all specialists.
		for n := range t.specialists {
			names = append(names, n)
		}
	}

	type result struct {
		name   string
		output string
		err    error
	}

	results := make(chan result, len(names))
	var wg sync.WaitGroup

	for _, name := range names {
		sr, ok := t.specialists[name]
		if !ok {
			return nil, fmt.Errorf("specialist %q not found", name)
		}
		wg.Add(1)
		go func(sr *SpecialistRunner) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results <- result{name: sr.Name, output: "", err: fmt.Errorf("specialist panic: %v", r)}
				}
			}()
			out, err := RunSubAgent(ctx, sr.Prov, sr.reg, sr.Prompt, task, Options{
				MaxSteps:      10,
				Temperature:   0.3,
				Pricing:       sr.Pricing,
				ContextWindow: sr.ContextWindow,
			}, t.sink)
			results <- result{name: sr.Name, output: out, err: err}
		}(sr)
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				// If wg.Wait panics, ensure results still closes so callers don't hang.
				close(results)
			}
		}()
		wg.Wait()
		close(results)
	}()

	aggregated := make(map[string]string, len(names))
	for r := range results {
		if r.err != nil {
			aggregated[r.name] = fmt.Sprintf("ERROR: %v", r.err)
		} else {
			aggregated[r.name] = r.output
		}
	}
	return aggregated, nil
}

// --- delegate tool ---

type delegateTool struct {
	name        string
	description string
	team        *Team
}

func (d *delegateTool) Name() string        { return "delegate_" + d.name }
func (d *delegateTool) ReadOnly() bool      { return false }
func (d *delegateTool) Description() string { return d.description }

func (d *delegateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"task":{"type":"string","description":"Subtask to delegate"}
		},
		"required":["task"]
	}`)
}

func (d *delegateTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Task string `json:"task"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Task == "" {
		return "", fmt.Errorf("task is required")
	}
	if d.team == nil {
		return "", fmt.Errorf("delegate tool not wired to a team")
	}
	sr, ok := d.team.specialists[d.name]
	if !ok {
		return "", fmt.Errorf("specialist %q not found", d.name)
	}
	return RunSubAgent(ctx, sr.Prov, sr.reg, sr.Prompt, p.Task, Options{
		MaxSteps:      10,
		Temperature:   0.3,
		Pricing:       sr.Pricing,
		ContextWindow: sr.ContextWindow,
	}, d.team.sink)
}

// --- parallel delegate tool: delegates to ALL specialists at once ---

// parallelDelegateTool lets the orchestrator dispatch a task to multiple
// specialists in parallel. Results are aggregated and returned as a single
// summary with per-specialist outputs labeled.
type parallelDelegateTool struct {
	team *Team
}

func (d *parallelDelegateTool) Name() string   { return "delegate_all" }
func (d *parallelDelegateTool) ReadOnly() bool { return false }
func (d *parallelDelegateTool) Description() string {
	return "Delegate a task to ALL specialists in parallel and aggregate their results. Use when you need multiple perspectives or when the task can be split across specialists."
}

func (d *parallelDelegateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"task":{"type":"string","description":"Task to send to all specialists in parallel"}
		},
		"required":["task"]
	}`)
}

func (d *parallelDelegateTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Task string `json:"task"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Task == "" {
		return "", fmt.Errorf("task is required")
	}
	if d.team == nil {
		return "", fmt.Errorf("delegate_all not wired to a team")
	}

	results, err := d.team.RunParallel(ctx, p.Task, nil)
	if err != nil {
		return "", err
	}

	// Format aggregation.
	out := "# Team Results (parallel)\n\n"
	for name, r := range results {
		out += fmt.Sprintf("## %s\n\n%s\n\n---\n\n", name, r)
	}
	return out, nil
}

// --- config parsing ---

type TeamConfig struct {
	Orchestrator string             `toml:"orchestrator"`
	Specialists  []SpecialistConfig `toml:"specialists"`
}

type SpecialistConfig struct {
	Name        string   `toml:"name"`
	Model       string   `toml:"model"`
	Description string   `toml:"description"`
	Prompt      string   `toml:"prompt"`
	Tools       []string `toml:"tools"`
}

// BuildTeam constructs a Team from config, creating both the orchestrator Agent
// and all specialists. The orchestrator gets delegate_<name> tools AND a
// delegate_all tool for parallel dispatch.
func BuildTeam(
	cfg TeamConfig,
	resolveModel func(string) (provider.Provider, *provider.Pricing, int, error),
	sink event.Sink,
	baseReg *tool.Registry,
	gate Gate,
	hooks ToolHooks,
	opts Options,
) (*Team, error) {
	if cfg.Orchestrator == "" {
		return nil, fmt.Errorf("team: orchestrator model is required")
	}
	if len(cfg.Specialists) == 0 {
		return nil, fmt.Errorf("team: at least one specialist is required")
	}

	var specialists []Specialist
	for _, sc := range cfg.Specialists {
		prov, pricing, ctxWin, err := resolveModel(sc.Model)
		if err != nil {
			return nil, fmt.Errorf("team specialist %q: %w", sc.Name, err)
		}
		prompt := sc.Prompt
		if prompt == "" {
			prompt = fmt.Sprintf("You are %s, a specialist agent. Complete your assigned task and return the result.", sc.Name)
		}
		specReg := FilterRegistry(baseReg, sc.Tools,
			"review", "security_review",
			"run_skill", "install_skill", "task", "ask", "computer-use", "tool-groups")
		specialists = append(specialists, Specialist{
			Name:          sc.Name,
			Description:   sc.Description,
			Model:         sc.Model,
			Prov:          prov,
			Pricing:       pricing,
			ContextWindow: ctxWin,
			Tools:         specReg.Names(),
			Reg:           specReg,
			Prompt:        prompt,
		})
	}

	t := NewTeam(nil, specialists, sink)

	orchProv, orchPricing, orchCtxWin, err := resolveModel(cfg.Orchestrator)
	if err != nil {
		return nil, fmt.Errorf("team orchestrator %q: %w", cfg.Orchestrator, err)
	}

	orchReg := tool.NewRegistry()
	for _, name := range baseReg.Names() {
		if tl, ok := baseReg.Get(name); ok {
			orchReg.Add(tl)
		}
	}
	for _, name := range t.tools.Names() {
		if tl, ok := t.tools.Get(name); ok {
			orchReg.Add(tl)
		}
	}
	// Add parallel dispatch tool.
	orchReg.Add(&parallelDelegateTool{team: t})

	orchPrompt := DefaultPlannerPrompt + "\n\nYou lead a team of specialists. Delegate sub-tasks using delegate_<name> tools, or use delegate_all to send a task to all specialists at once for parallel execution."
	orchSess := NewSession(orchPrompt)
	orchestrator := New(orchProv, orchReg, orchSess, Options{
		MaxSteps:      opts.MaxSteps,
		Temperature:   opts.Temperature,
		Pricing:       orchPricing,
		Gate:          gate,
		Hooks:         hooks,
		ContextWindow: orchCtxWin,
		ArchiveDir:    opts.ArchiveDir,
	}, sink)

	t.Orchestrator = orchestrator
	return t, nil
}
