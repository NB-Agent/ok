package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/NB-Agent/ok/internal/agent"
	"github.com/NB-Agent/ok/internal/log"
)

func agentCommand(args []string) int {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: ok agent <list|install|publish>")
		return 2
	}

	cmd := fs.Arg(0)
	switch cmd {
	case "list":
		return agentList(*jsonOut)
	case "install":
		return agentInstall(fs.Args()[1:])
	case "publish":
		return agentPublish(fs.Args()[1:])
	default:
		log.Error("unknown agent command", "cmd", cmd)
		return 2
	}
}

func agentList(jsonOut bool) int {
	entries, err := agent.ListAgents(context.Background(), "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent list:", err)
		return 1
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(entries); err != nil {
			fmt.Fprintf(os.Stderr, "agent list: json encode: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Println("Available agents:")
	for _, e := range entries {
		fmt.Printf("  %-20s %s", e.Name, e.Description)
		if e.Author != "" {
			fmt.Printf(" (author: %s)", e.Author)
		}
		fmt.Println()
	}
	return 0
}

func agentInstall(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ok agent install <name>")
		return 2
	}
	name := args[0]
	entries, err := agent.ListAgents(context.Background(), "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent install:", err)
		return 1
	}
	var found *agent.StoreEntry
	for _, e := range entries {
		if e.Name == name {
			found = &e
			break
		}
	}
	if found == nil {
		log.Error("agent not found", "name", name)
		return 1
	}
	cwd, _ := os.Getwd()
	if err := agent.InstallAgent(context.Background(), cwd, found.Name, found.URL); err != nil {
		fmt.Fprintln(os.Stderr, "agent install:", err)
		return 1
	}
	return 0
}

func agentPublish(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ok agent publish <agent.md>")
		return 2
	}
	path := args[0]
	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent publish:", err)
		return 1
	}
	// Create a minimal AgentDef from the file
	name := strings.TrimSuffix(path, ".md")
	if idx := strings.LastIndex(name, string(os.PathSeparator)); idx >= 0 {
		name = name[idx+1:]
	}
	def := agent.AgentDef{Name: name, SystemPrompt: string(content)}
	fmt.Println(agent.PublishAgent(def))
	return 0
}
