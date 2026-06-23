package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/NB-Agent/ok/internal/boot"
	"github.com/NB-Agent/ok/internal/codegraph"
	"github.com/NB-Agent/ok/internal/config"
	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/mcpserver"
	"github.com/NB-Agent/ok/internal/plugin"
	"github.com/NB-Agent/ok/internal/sandbox"
	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/tool/builtin"
)

// mcp.go holds the MCP server-management surface shared by the `ok mcp`
// subcommand (config-only; takes effect next session) and the in-chat `/mcp add`
// / `/mcp remove` slash commands (which hot-connect via the controller). Both
// parse arguments through parseMCPAdd so the grammar is identical everywhere.

// parseMCPAdd turns the arguments after "add" into a config.PluginEntry. Grammar:
//
//	<name> [--http URL | --sse URL] [--env K=V]... [--header K=V]... [command [args...]]
//
// A --http/--sse URL makes it a remote server; otherwise the first non-flag token
// (after the name and any --env/--header flags) begins the stdio command, and the
// rest are its args verbatim — so the command keeps its own -flags (e.g. `npx -y
// pkg`). Flag values accept both "--http URL" and "--http=URL" forms.
func parseMCPAdd(args []string) (config.PluginEntry, error) {
	var e config.PluginEntry
	if len(args) == 0 {
		return e, fmt.Errorf("mcp add: missing server name")
	}
	e.Name = strings.TrimSpace(args[0])
	if e.Name == "" || strings.HasPrefix(e.Name, "-") {
		return e, fmt.Errorf("mcp add: first argument must be the server name, got %q", args[0])
	}
	rest := args[1:]

	i := 0
	// next consumes the following token as a flag's value (for the "--flag value"
	// form), reporting false when none remains.
	next := func(flag string) (string, error) {
		if i+1 >= len(rest) {
			return "", fmt.Errorf("mcp add: %s needs a value", flag)
		}
		i++
		return rest[i], nil
	}
	setEnv := func(dst *map[string]string, flag, pair string) error {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return fmt.Errorf("mcp add: %s expects KEY=VALUE, got %q", flag, pair)
		}
		if *dst == nil {
			*dst = map[string]string{}
		}
		(*dst)[k] = v
		return nil
	}

	for ; i < len(rest); i++ {
		a := rest[i]
		key, inline, hasInline := strings.Cut(a, "=")
		switch {
		case !strings.HasPrefix(a, "-"):
			// The stdio command and its remaining args, verbatim.
			e.Command = a
			e.Args = append([]string(nil), rest[i+1:]...)
			i = len(rest)
		case key == "--http" || key == "--streamable-http":
			v := inline
			if !hasInline {
				var err error
				if v, err = next(key); err != nil {
					return e, err
				}
			}
			e.Type, e.URL = "http", v
		case key == "--sse":
			v := inline
			if !hasInline {
				var err error
				if v, err = next(key); err != nil {
					return e, err
				}
			}
			e.Type, e.URL = "sse", v
		case key == "--env" || key == "--header":
			pair := inline
			if !hasInline {
				var err error
				if pair, err = next(key); err != nil {
					return e, err
				}
			}
			dst := &e.Env
			if key == "--header" {
				dst = &e.Headers
			}
			if err := setEnv(dst, key, pair); err != nil {
				return e, err
			}
		default:
			return e, fmt.Errorf("mcp add: unknown flag %q", a)
		}
	}

	switch {
	case e.URL != "" && e.Command != "":
		return e, fmt.Errorf("mcp add: specify a command OR a --http/--sse URL, not both")
	case e.URL == "" && e.Command == "":
		return e, fmt.Errorf("mcp add: need a command (stdio) or a --http/--sse URL")
	}
	return e, nil
}

// tokenizeArgs splits a slash-command line into arguments, honoring "double" and
// 'single' quotes so values with spaces (e.g. --header "Authorization=Bearer x")
// survive. An unterminated quote takes the rest of the line as one token.
func tokenizeArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inWord := false
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			inWord = true
		case r == '"' || r == '\'':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

// mcpCommand implements `ok mcp <add|remove|list>`. It edits config only
// (validate → UpsertPlugin/RemovePlugin → Save); the server connects on the next
// session start. For a live connect inside an open chat, use `/mcp add`.
func mcpCommand(args []string) int {
	if len(args) == 0 {
		mcpUsage()
		return 2
	}
	switch args[0] {
	case "list", "ls":
		return mcpList()
	case "serve":
		return mcpServe(args[1:])
	case "add":
		return mcpAddCLI(args[1:])
	case "remove", "rm":
		return mcpRemoveCLI(args[1:])
	case "help", "-h", "--help":
		mcpUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown mcp subcommand %q\n\n", args[0])
		mcpUsage()
		return 2
	}
}

func mcpList() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	listed := 0
	// CodeGraph is a built-in server injected by boot, not a [[plugins]] entry, so
	// report its resolved status here too — otherwise `mcp list` looks empty even
	// when codegraph will load. This doubles as a headless preflight: it shows
	// whether the binary resolves before you enter a session.
	if cfg.Codegraph.Enabled {
		if bin, ok := codegraph.Resolve(cfg.Codegraph.Path); ok {
			fmt.Printf("%-16s (stdio, built-in)  %s serve --mcp\n", "codegraph", bin)
		} else {
			fmt.Printf("%-16s (built-in, unavailable)  binary not found — ship it beside ok, install on PATH, or set [codegraph].path\n", "codegraph")
		}
		listed++
	}
	for _, p := range cfg.Plugins {
		typ := p.Type
		if typ == "" {
			typ = "stdio"
		}
		if typ == "stdio" {
			line := strings.TrimSpace(p.Command + " " + strings.Join(p.Args, " "))
			fmt.Printf("%-16s (stdio)  %s\n", p.Name, line)
		} else {
			fmt.Printf("%-16s (%s)  %s\n", p.Name, typ, p.URL)
		}
		listed++
	}
	if listed == 0 {
		fmt.Println("no MCP servers configured")
	}
	return 0
}

func mcpAddCLI(args []string) int {
	entry, err := parseMCPAdd(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := cfg.UpsertPlugin(entry); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := cfg.Save(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("added MCP server %q — loads on the next session (or run `/mcp add` inside chat to connect it live now)\n", entry.Name)
	return 0
}

func mcpRemoveCLI(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ok mcp remove <name>")
		return 2
	}
	name := args[0]
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !cfg.RemovePlugin(name) {
		fmt.Fprintf(os.Stderr, "no MCP server named %q in config\n", name)
		return 1
	}
	if err := cfg.Save(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("removed MCP server %q\n", name)
	return 0
}

// mcpServe builds the tool registry from the current config and exposes it as
// an MCP server over stdio. Other coding agents (Claude Code, Cursor, etc.)
// can connect and call ok's tools (bash, read_file, grep, glob, etc.).
func mcpServe(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// Build the tool registry: all built-in tools, confined to workspace.
	reg := tool.NewRegistry()
	for _, t := range tool.Builtins() {
		reg.Add(t)
	}
	// Confine file-writers and bash — same as boot.addBuiltins.
	bashSpec := sandbox.Spec{Mode: cfg.Sandbox.Bash, WriteRoots: cfg.WriteRoots(), Network: cfg.Sandbox.Network}
	writeRoots := cfg.WriteRoots()
	readRoots := cfg.ReadRoots()
	confined := append(builtin.ConfineWriters(writeRoots),
		append(builtin.ConfineReaders(readRoots), builtin.ConfineBash(bashSpec))...)
	for _, t := range confined {
		if _, ok := reg.Get(t.Name()); ok {
			reg.Add(t)
		}
	}

	// Add plugin tools (MCP servers).
	specs := boot.PluginSpecs(cfg.Plugins)
	if len(specs) > 0 {
		host, ptools, err := plugin.StartAll(context.Background(), specs)
		if err == nil && host != nil {
			for _, t := range ptools {
				reg.Add(t)
			}
			defer log.CloseSimple("MCP host", host)
		}
	}

	provider := mcpserver.NewRegistryAdapter(reg)
	fmt.Fprintf(os.Stderr, "OK MCP server starting with %d tools\n", len(provider.ListTools()))
	return runMCP(provider)
}

func runMCP(provider *mcpserver.RegistryAdapter) int {
	s := mcpserver.New(os.Stdin, os.Stdout, provider)
	ctx := context.Background()
	if err := s.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "mcp serve error: %v\n", err)
		return 1
	}
	return 0
}

func mcpUsage() {
	fmt.Println(`Manage and serve MCP servers.

Usage:
  ok mcp list                              list configured servers
  ok mcp add <name> <command> [args...]    add a stdio server
  ok mcp add <name> --http <url> [...]     add a remote server
  ok mcp remove <name>                     remove a server
  ok mcp serve                             serve OK's tools as an MCP server over stdio

Examples:
  ok mcp add fs npx -y @modelcontextprotocol/server-filesystem .
  ok mcp serve        # any MCP host (Claude Code, Cursor, ...) can now call ok's tools

Changes take effect on the next session; inside a running chat, use /mcp add to
connect a server live.`)
}
