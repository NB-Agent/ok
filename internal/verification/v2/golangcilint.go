package v2

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

var gciRe = regexp.MustCompile(`^(.+?):(\d+):(\d+)?:?\s+(.+?)\s+\((.+?)\)$`)

type golangciLintAnalyzer struct{}

func (golangciLintAnalyzer) Name() string        { return "golangci-lint" }
func (golangciLintAnalyzer) Layer() string       { return "external" }
func (golangciLintAnalyzer) Languages() []string { return []string{"go"} }
func (golangciLintAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	if !which("golangci-lint") {
		return nil, nil
	}
	out, err := runCmd(ctx, root, "golangci-lint", "run", "--out-format=line-number", "--timeout=2m", "./...")
	if err != nil {
		_ = err
	}
	return parseGolangciLint(out), nil
}

func parseGolangciLint(output string) []Finding {
	var ff []Finding
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := gciRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lno, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		ff = append(ff, Finding{
			Analyzer: "golangci-lint",
			Layer:    "external",
			Severity: golangciSev(m[5]),
			File:     m[1],
			Line:     lno,
			Column:   col,
			Message:  m[4],
			Category: golangciCat(m[5]),
			Rule:     m[5],
		})
	}
	return ff
}

func golangciSev(linter string) Severity {
	switch {
	case strings.Contains(linter, "gosec") || strings.HasPrefix(linter, "G"):
		return SevHigh
	case strings.Contains(linter, "errcheck") || strings.Contains(linter, "govet") ||
		strings.Contains(linter, "staticcheck") || strings.Contains(linter, "ineffassign"):
		return SevMedium
	case strings.Contains(linter, "revive") || strings.Contains(linter, "stylecheck"):
		return SevLow
	default:
		return SevInfo
	}
}

func golangciCat(linter string) string {
	switch {
	case strings.Contains(linter, "gosec") || strings.HasPrefix(linter, "G"):
		return "security"
	case strings.Contains(linter, "errcheck") || strings.Contains(linter, "ineffassign"):
		return "correctness"
	case strings.Contains(linter, "revive") || strings.Contains(linter, "stylecheck"):
		return "style"
	default:
		return "general"
	}
}
