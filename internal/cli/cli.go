// Package cli implements ok's command-line entry: subcommand routing, flag
// parsing, assembly from config, and exit codes. The core is config-driven —
// providers and tools are resolved from configuration, not hardcoded.
//
// ─── File organization ────────────────────────────────────────────────────
//
//	cli.go       — root: flag parsing, subcommand routing, Run()
//	acp.go       — ACP stdio JSON-RPC server
//	chat*.go     — chat REPL + Bubbletea TUI model
//	complete.go  — tab-completion (slash commands, args)
//	event_handler— TUI event dispatch (approval keys, model, skills)
//	helpers.go   — TUI helpers (balance, events, rendering updates)
//	msg.go       — TUI message types
//	render.go    — TUI rendering helpers
//	slash.go     — slash-command dispatch (/model, /mcp, /skills)
//	select.go    — interactive single/multi-select menus
//	style.go     — ANSI style constants + color detection
//	theme.go     — JSON theme system
//	box.go       — ANSI-aware terminal width + border rendering
//	md.go        — Markdown → ANSI renderer
//	*             — one-file subcommands (audit, ci, demo, doctor, mcp,
//	                review, run, session, setup, update, welcome, memory,
//	                model, agent_cmd, skill_hooks)
//	chooser.go   — multi-select component for ask tool
//
// The TUI files (chat_tui, event_handler, helpers, msg, render, select,
// chooser, complete) form a natural cli/tui subpackage candidate.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/NB-Agent/ok/internal/boot"
	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/control"
	"github.com/NB-Agent/ok/internal/event"
	"github.com/NB-Agent/ok/internal/i18n"
	"github.com/NB-Agent/ok/internal/log"

	"golang.org/x/term"
)

// eventChannelCap buffers the TUI's event channel so streaming bursts (long tool
// results, multi-message answers) don't backpressure the agent goroutine.
const eventChannelCap = 1024

// cliVersion stores the build version for use across subcommands.
var cliVersion string

// Run is the CLI entry point; it returns a process exit code.
func Run(args []string, version string) int {
	cliVersion = version
	// Pick the UI language up front so even pre-config paths (the first-run
	// welcome banner) come through localized. Env-only first; if a config
	// exists and pins a language, that wins.
	i18n.DetectLanguage("")
	if cfg, err := config.Load(); err == nil && cfg.Language != "" {
		i18n.DetectLanguage(cfg.Language)
	}

	if len(args) == 0 {
		return welcome(version)
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "run":
		return runAgent(rest)
	case "chat":
		return chatREPL(rest)
	case "serve":
		return runServe(rest)
	case "setup":
		return setupConfig(rest)
	case "init":
		// Project memory (AGENTS.md) is model-generated in-session — `/init` runs
		// the codebase analysis. This CLI entry just points there (and to `setup`
		// for config), so `ok init` isn't a dead end.
		return initHint()
	case "acp":
		return acpCommand(rest, version)
	case "mcp":
		return mcpCommand(rest)
	case "update":
		return updateCommand(rest, version)
	case "session":
		return sessionCommand(rest)
	case "doctor":
		fmt.Print(doctor())
		return 0
	case "demo":
		return demoRun(rest)
	case "ci":
		return ciRun(rest, version)
	case "review":
		return reviewCommand(rest, version)
	case "audit":
		return auditRun(rest)
	case "agent":
		return agentCommand(rest)
	case "version", "--version", "-v":
		fmt.Println("ok", version)
		return 0
	case "help", "--help", "-h":
		usage()
		return 0
	default:
		log.Error("unknown command", "cmd", cmd)
		usage()
		return 2
	}
}

// setup builds a ready-to-drive Controller from config via boot.Build. It is a
// thin adapter kept so the subcommands below read the same as before; the actual
// assembly (model resolution, tool registry, permission gate, two-model
// Coordinator) lives in internal/boot, shared with the desktop frontend.
// requireKey forces the executor's API key to be present (used by run); chat
// passes false so the session UI is reachable before a key is set. sink receives
// the agent's typed event stream — runAgent passes a TextSink that renders to
// stdout, the TUI passes an event-channel sink so events become tea.Msgs.
func setup(ctx context.Context, modelName string, maxStepsOverride int, requireKey bool, sink event.Sink) (*control.Controller, error) {
	return boot.Build(ctx, boot.Options{
		Model:      modelName,
		MaxSteps:   maxStepsOverride,
		RequireKey: requireKey,
		Sink:       sink,
	})
}

// isInteractive reports whether we're attached to a real terminal on both
// stdin and stdout — required for prompting. Redirected or piped I/O is not
// interactive, so wizards never block or auto-default in scripts and CI.
func isInteractive() bool {
	return isTTY(os.Stdin) && isTTY(os.Stdout)
}

func isTTY(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// readStdin reads piped input if present; an interactive terminal yields "".
func readStdin() string {
	stat, err := os.Stdin.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice != 0 {
		return ""
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func usage() {
	fmt.Print(i18n.M.UsageBody)
}
