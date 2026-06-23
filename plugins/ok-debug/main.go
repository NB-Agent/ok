// @ok/debug — MCP plugin: Delve (dlv) Go debugger. (migrated to plugin.StdioServer)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/NB-Agent/ok/internal/plugin"
)

// --- harness-backed server ---

type server struct{}

func (server) Info() (string, string) { return "ok-debug", "1.0.0" }

func (server) Tools() []plugin.ToolDef {
	return []plugin.ToolDef{{
		Name:        "debug",
		Description: "Debug Go programs using Delve (dlv)",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": plugin.StrEnum("start", "break", "continue", "next", "step", "print", "stack", "locals", "list", "restart", "stop", "set"),
				"target": plugin.StrProp(),
				"args":   plugin.StrProp(),
			},
			"required": []string{"action"},
		},
	}}
}

func (server) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	if name != "debug" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	var p struct {
		Action   string `json:"action"`
		Target   string `json:"target"`
		ProgArgs string `json:"args"`
	}
	json.Unmarshal(args, &p)
	noArg := map[string]bool{"continue": true, "next": true, "step": true, "stack": true, "locals": true, "restart": true, "stop": true}
	if noArg[p.Action] {
		return runDlv(p.Action)
	}
	if p.Action == "list" {
		return runDlv(append([]string{"list"}, strings.Fields(p.Target)...)...)
	}
	if p.Action == "set" {
		return runDlv("set", p.Target)
	}
	return runDlv(p.Action, p.Target)
}

func runDlv(args ...string) (string, error) {
	// Prepend -- to prevent user-controlled values from being parsed as dlv options.
	out, err := exec.Command("dlv", append([]string{"--"}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func main() { plugin.RunStdio(server{}) }
