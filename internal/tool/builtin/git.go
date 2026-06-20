package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/winhide"
)

func init() { tool.RegisterBuiltin(gitTool{}) }

type gitTool struct{}

func (gitTool) Name() string { return "git" }

func (gitTool) Description() string {
	return "Run Git operations — status, diff, log, commit, branch, merge, pull, push, stash, and more. Handles structured output and common workflows."
}

func (gitTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"action":{"enum":["status","diff","log","commit","add","branch","checkout","merge","pull","push","stash","init","clone","remote","tag","reset","rebase","show","blame","shortlog"],"type":"string"},"args":{"type":"string"},"path":{"type":"string"}},"required":["action"],"type":"object"}`)
}

func (gitTool) ReadOnly() bool { return false }

func (gitTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Action string `json:"action"`
		Args   string `json:"args"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Action == "" {
		return "", fmt.Errorf("action is required")
	}

	gitArgs := []string{p.Action}
	if p.Args != "" {
		gitArgs = append(gitArgs, splitArgs(p.Args)...)
	}

	// Append structured flags for parseable actions.
	switch p.Action {
	case "status":
		gitArgs = append(gitArgs, "--branch", "--porcelain")
	case "log":
		if !containsFlag(gitArgs, "--oneline") && !containsFlag(gitArgs, "--format") {
			gitArgs = append(gitArgs, "--oneline", "--graph", "-20")
		}
	case "diff":
		if !containsFlag(gitArgs, "--stat") && !containsFlag(gitArgs, "--name-only") {
			gitArgs = append(gitArgs, "--stat")
		}
	case "branch":
		if !containsFlag(gitArgs, "-a") && !containsFlag(gitArgs, "--list") && p.Args == "" {
			gitArgs = append(gitArgs, "-a")
		}
	case "stash":
		if len(gitArgs) == 1 {
			gitArgs = append(gitArgs, "list")
		}
	case "remote":
		if len(gitArgs) == 1 {
			gitArgs = append(gitArgs, "-v")
		}
	case "shortlog":
		if !containsFlag(gitArgs, "-s") && !containsFlag(gitArgs, "--summary") {
			gitArgs = append(gitArgs, "-sn", "--all")
		}
	default:
		return "", fmt.Errorf("git: unknown action %q", p.Action)
	}

	cmd := winhide.CommandContext(ctx, "git", gitArgs...)
	if p.Path != "" {
		cmd.Dir = p.Path
	}

	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Git %s\n\n", p.Action))
	if err != nil {
		// git writes errors to stderr which CombinedOutput includes
		b.WriteString(fmt.Sprintf("⚠️  %v\n\n", err))
	}
	if output != "" {
		b.WriteString("```\n" + output + "\n```\n")
	}
	return b.String(), nil
}

func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	quoteChar := byte(0)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inQuote {
			if ch == quoteChar {
				inQuote = false
			} else {
				cur.WriteByte(ch)
			}
			continue
		}
		switch ch {
		case '\'', '"':
			inQuote = true
			quoteChar = ch
		case ' ':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
