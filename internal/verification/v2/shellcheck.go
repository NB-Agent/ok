package v2

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

var schRe = regexp.MustCompile(`^(.+?):(\d+):(\d+):\s+(error|warning|note|info):\s+(.+?)\s+\[(SC\d+)\]$`)

type shellcheckAnalyzer struct{}

func (shellcheckAnalyzer) Name() string        { return "shellcheck" }
func (shellcheckAnalyzer) Layer() string       { return "external" }
func (shellcheckAnalyzer) Languages() []string { return []string{"shell", "bash"} }

func (shellcheckAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	if !which("shellcheck") {
		return nil, nil
	}
	out, err := runCmd(ctx, root, "sh", "-c", "find . -name '*.sh' -type f | head -200 | xargs shellcheck -f gcc 2>/dev/null || true")
	if err != nil {
		_ = err
	}
	return parseShellcheck(out), nil
}

func parseShellcheck(output string) []Finding {
	var ff []Finding
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := schRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lno, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		sev := SevLow
		switch m[4] {
		case "error":
			sev = SevHigh
		case "warning":
			sev = SevMedium
		}
		ff = append(ff, Finding{
			Analyzer: "shellcheck",
			Layer:    "external",
			Severity: sev,
			File:     m[1],
			Line:     lno,
			Column:   col,
			Message:  m[5],
			Category: "shell",
			Rule:     m[6],
		})
	}
	return ff
}
