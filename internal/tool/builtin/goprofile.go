package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/winhide"
)

func init() { tool.RegisterBuiltin(goProfile{}) }

// goProfile wraps pprof for CPU/memory/heap profile analysis. It runs a Go
// benchmark or test under profiling, then extracts the top hotspots.
type goProfile struct{}

func (goProfile) Name() string { return "go-profile" }

func (goProfile) Description() string {
	return "Profile Go code with pprof — CPU, heap, allocs, or goroutine. Runs on a test or benchmark; reports top hotspots."
}

func (goProfile) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"benchmark":{"type":"string"},"profile":{"enum":["cpu","heap","allocs","goroutine"],"type":"string"},"target":{"type":"string"}},"required":["profile"],"type":"object"}`)
}

func (goProfile) ReadOnly() bool { return true }

func (goProfile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Profile   string `json:"profile"`
		Target    string `json:"target"`
		Benchmark string `json:"benchmark"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Target == "" {
		p.Target = "./..."
	}

	// Create temp dir for profiles
	tmpDir, err := os.MkdirTemp("", "ok-profile-*")
	if err != nil {
		return "", fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	profileFile := filepath.Join(tmpDir, "profile.out")

	var cmd *exec.Cmd
	switch p.Profile {
	case "cpu":
		if p.Benchmark != "" {
			cmd = winhide.CommandContext(ctx, "go", "test", p.Target, "-bench", p.Benchmark, "-cpuprofile", profileFile, "-benchtime", "1s")
		} else {
			cmd = winhide.CommandContext(ctx, "go", "test", p.Target, "-cpuprofile", profileFile, "-count=1", "-timeout", "60s")
		}
	case "heap", "allocs":
		if p.Benchmark != "" {
			cmd = winhide.CommandContext(ctx, "go", "test", p.Target, "-bench", p.Benchmark, "-memprofile", profileFile, "-benchtime", "1s")
		} else {
			cmd = winhide.CommandContext(ctx, "go", "test", p.Target, "-memprofile", profileFile, "-count=1", "-timeout", "60s")
		}
	case "goroutine":
		// For goroutines, use the goroutine profile from a running test
		cmd = winhide.CommandContext(ctx, "go", "test", p.Target, "-count=1", "-timeout", "30s")
	default:
		return "", fmt.Errorf("unsupported profile: %s", p.Profile)
	}

	cmdOut, err := cmd.CombinedOutput()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Go Profile: %s\n", p.Profile))
	b.WriteString(fmt.Sprintf("## Target: %s\n\n", p.Target))

	if err != nil {
		b.WriteString(fmt.Sprintf("⚠️ Test run had errors:\n```\n%s\n```\n\n", truncateStr(string(cmdOut), 500)))
	}

	// Parse profile with go tool pprof -top
	if _, statErr := os.Stat(profileFile); statErr == nil {
		topCmd := winhide.CommandContext(ctx, "go", "tool", "pprof", "-top", "-nodecount=20", profileFile)
		topOut, topErr := topCmd.CombinedOutput()
		if topErr == nil {
			b.WriteString("## Top Hotspots\n\n")
			b.WriteString("```\n")
			lines := strings.Split(string(topOut), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "File:") {
					b.WriteString(line + "\n")
				}
			}
			b.WriteString("```\n")
		} else {
			b.WriteString(fmt.Sprintf("(pprof parse: %s)\n", topErr))
		}

		// Also show top functions by cumulative time
		listCmd := winhide.CommandContext(ctx, "go", "tool", "pprof", "-list", ".*", "-nodecount=10", profileFile)
		listOut, _ := listCmd.CombinedOutput()
		hotFunctions := extractHotFunctions(string(listOut))
		if len(hotFunctions) > 0 {
			b.WriteString("\n## Hot Functions\n\n")
			for _, f := range hotFunctions {
				b.WriteString(fmt.Sprintf("- %s\n", f))
			}
		}
	} else {
		b.WriteString("(no profile data generated — test may have failed before profiling)\n")
	}

	return b.String(), nil
}

func extractHotFunctions(output string) []string {
	var funcs []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// pprof -list output shows functions with total time
		if strings.Contains(line, "Total:") && len(funcs) < 15 {
			if colonIdx := strings.Index(line, ":"); colonIdx >= 0 {
				funcs = append(funcs, strings.TrimSpace(line[colonIdx+1:]))
			}
		}
	}
	return funcs
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
