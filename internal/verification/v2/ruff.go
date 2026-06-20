package v2

import (
	"context"
	"encoding/json"
	"strings"
)

type ruffAnalyzer struct{}

func (ruffAnalyzer) Name() string        { return "ruff" }
func (ruffAnalyzer) Layer() string       { return "external" }
func (ruffAnalyzer) Languages() []string { return []string{"python"} }

func (ruffAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	if !which("ruff") {
		return nil, nil
	}
	out, err := runCmd(ctx, root, "ruff", "check", "--output-format=json", ".")
	if err != nil {
		_ = err
	}
	return parseRuff(out), nil
}

func parseRuff(output string) []Finding {
	type ruffIssue struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		Filename string `json:"filename"`
		Location struct {
			Row    int `json:"row"`
			Column int `json:"column"`
		} `json:"location"`
		Fix struct {
			Content string `json:"content"`
		} `json:"fix"`
	}
	var issues []ruffIssue
	if err := json.Unmarshal([]byte(output), &issues); err != nil {
		return nil
	}
	var ff []Finding
	for _, iss := range issues {
		ff = append(ff, Finding{
			Analyzer: "ruff",
			Layer:    "external",
			Severity: ruffSev(iss.Code),
			File:     iss.Filename,
			Line:     iss.Location.Row,
			Column:   iss.Location.Column,
			Message:  iss.Code + ": " + iss.Message,
			Category: ruffCat(iss.Code),
			Fix:      iss.Fix.Content,
			Rule:     iss.Code,
		})
	}
	return ff
}

func ruffSev(code string) Severity {
	switch {
	case strings.HasPrefix(code, "S"):
		return SevHigh
	case strings.HasPrefix(code, "F"), strings.HasPrefix(code, "B"):
		return SevMedium
	case strings.HasPrefix(code, "E"), strings.HasPrefix(code, "W"),
		strings.HasPrefix(code, "D"), strings.HasPrefix(code, "N"),
		strings.HasPrefix(code, "UP"), strings.HasPrefix(code, "C4"),
		strings.HasPrefix(code, "SIM"), strings.HasPrefix(code, "PTH"):
		return SevLow
	default:
		return SevInfo
	}
}

func ruffCat(code string) string {
	switch {
	case strings.HasPrefix(code, "S"):
		return "security"
	case strings.HasPrefix(code, "F"), strings.HasPrefix(code, "B"):
		return "correctness"
	case strings.HasPrefix(code, "E"), strings.HasPrefix(code, "W"), strings.HasPrefix(code, "D"):
		return "style"
	case strings.HasPrefix(code, "C4"), strings.HasPrefix(code, "SIM"), strings.HasPrefix(code, "UP"):
		return "performance"
	case strings.HasPrefix(code, "I"):
		return "imports"
	default:
		return "general"
	}
}
