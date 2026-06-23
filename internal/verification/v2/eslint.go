package v2

import (
	"context"
	"encoding/json"
	"strings"
)

type eslintAnalyzer struct{}

func (eslintAnalyzer) Name() string        { return "eslint" }
func (eslintAnalyzer) Layer() string       { return "external" }
func (eslintAnalyzer) Languages() []string { return []string{"javascript", "typescript"} }

func (eslintAnalyzer) Run(ctx context.Context, root string) ([]Finding, error) {
	if !which("npx") && !which("eslint") {
		return nil, nil
	}
	cmd := "eslint"
	args := []string{"--format=json", "--ext=.js,.jsx,.ts,.tsx,.mjs,.cjs", "."}
	if !which("eslint") {
		cmd = "npx"
		args = append([]string{"eslint"}, args...)
	}
	out, err := runCmd(ctx, root, cmd, args...)
	if err != nil {
		_ = err
	}
	return parseEslint(out), nil
}

func parseEslint(output string) []Finding {
	type eFile struct {
		FilePath string `json:"filePath"`
		Messages []struct {
			RuleID   string `json:"ruleId"`
			Message  string `json:"message"`
			Severity int    `json:"severity"`
			Line     int    `json:"line"`
			Column   int    `json:"column"`
			Fix      struct {
				Text string `json:"text"`
			} `json:"fix"`
		} `json:"messages"`
	}
	var files []eFile
	if err := json.Unmarshal([]byte(output), &files); err != nil {
		return nil
	}
	var ff []Finding
	for _, file := range files {
		for _, m := range file.Messages {
			sev := SevLow
			if m.Severity >= 2 {
				sev = SevMedium
			}
			ff = append(ff, Finding{
				Analyzer: "eslint",
				Layer:    "external",
				Severity: sev,
				File:     file.FilePath,
				Line:     m.Line,
				Column:   m.Column,
				Message:  m.Message,
				Category: eslintCat(m.RuleID),
				Fix:      m.Fix.Text,
				Rule:     m.RuleID,
			})
		}
	}
	return ff
}

func eslintCat(ruleID string) string {
	id := strings.ToLower(ruleID)
	if strings.Contains(id, "security") || strings.Contains(id, "xss") || strings.Contains(id, "no-eval") {
		return "security"
	}
	if strings.Contains(id, "no-unused") || strings.Contains(id, "no-undef") {
		return "correctness"
	}
	if strings.Contains(id, "prettier") || strings.Contains(id, "format") || strings.Contains(id, "indent") {
		return "style"
	}
	return "general"
}
