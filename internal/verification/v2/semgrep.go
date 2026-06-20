package v2

import (
	"context"
	"encoding/json"
	"strings"
)

type semgrepAnalyzer struct{}

func (semgrepAnalyzer) Name() string        { return "semgrep" }
func (semgrepAnalyzer) Layer() string       { return "external" }
func (semgrepAnalyzer) Languages() []string { return []string{"*"} }

func (semgrepAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	if !which("semgrep") {
		return nil, nil
	}
	out, err := runCmd(ctx, root, "semgrep", "scan", "--config=auto", "--json", "--quiet", "--no-git-ignore")
	if err != nil {
		_ = err
	}
	return parseSemgrep(out), nil
}

func parseSemgrep(output string) []Finding {
	type semResult struct {
		Results []struct {
			CheckID string `json:"check_id"`
			Path    string `json:"path"`
			Start   struct {
				Line int `json:"line"`
				Col  int `json:"col"`
			} `json:"start"`
			Extra struct {
				Message  string `json:"message"`
				Severity string `json:"severity"`
				Metadata struct {
					Category string `json:"category"`
				} `json:"metadata"`
				Fix string `json:"fix"`
			} `json:"extra"`
		} `json:"results"`
	}
	var data semResult
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return nil
	}
	var ff []Finding
	for _, r := range data.Results {
		ff = append(ff, Finding{
			Analyzer: "semgrep",
			Layer:    "external",
			Severity: semgrepSev(r.Extra.Severity),
			File:     r.Path,
			Line:     r.Start.Line,
			Column:   r.Start.Col,
			Message:  r.Extra.Message,
			Category: semgrepCat(r.CheckID, r.Extra.Metadata.Category),
			Fix:      r.Extra.Fix,
			Rule:     r.CheckID,
		})
	}
	return ff
}

func semgrepSev(s string) Severity {
	switch strings.ToUpper(s) {
	case "ERROR":
		return SevHigh
	case "WARNING":
		return SevMedium
	case "INFO":
		return SevLow
	default:
		return SevInfo
	}
}

func semgrepCat(ruleID, metaCat string) string {
	if metaCat != "" {
		return metaCat
	}
	id := strings.ToLower(ruleID)
	if strings.Contains(id, "security") || strings.Contains(id, "xss") || strings.Contains(id, "injection") {
		return "security"
	}
	if strings.Contains(id, "performance") {
		return "performance"
	}
	if strings.Contains(id, "style") || strings.Contains(id, "format") {
		return "style"
	}
	return "general"
}
