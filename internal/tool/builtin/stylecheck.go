package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/winhide"
)

func init() { tool.RegisterBuiltin(styleCheck{}) }

// styleCheck validates Go source files against project style conventions:
// - gofmt/goimports formatting
// - go vet correctness
// - naming conventions (from surrounding code)
// - error handling patterns
// - import grouping
type styleCheck struct{}

func (styleCheck) Name() string { return "style-check" }

func (styleCheck) Description() string {
	return "Validate Go code: formatting (gofmt), vet, naming conventions, error handling, import grouping."
}

func (styleCheck) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"check":{"enum":["format","vet","naming","all"],"type":"string"},"files":{"type":"string"}},"type":"object"}`)
}

func (styleCheck) ReadOnly() bool { return true }

func (styleCheck) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Files string `json:"files"`
		Check string `json:"check"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Files == "" {
		return "", fmt.Errorf("files is required (glob pattern)")
	}
	if p.Check == "" {
		p.Check = "all"
	}

	// Expand glob
	matches, err := filepath.Glob(p.Files)
	if err != nil || len(matches) == 0 {
		return "", fmt.Errorf("no files matched: %s", p.Files)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Style Check: %d file(s)\n\n", len(matches)))
	b.WriteString(fmt.Sprintf("Files: %s\n\n", strings.Join(matches, ", ")))

	issues := 0

	if p.Check == "format" || p.Check == "all" {
		b.WriteString("## Format (gofmt)\n\n")
		cmd := winhide.CommandContext(ctx, "gofmt", append([]string{"-d"}, matches...)...)
		out, _ := cmd.CombinedOutput()
		if len(out) == 0 {
			b.WriteString("✅ All files properly formatted\n\n")
		} else {
			diff := string(out)
			lineCount := strings.Count(diff, "\n")
			b.WriteString(fmt.Sprintf("❌ %d line(s) differ from gofmt:\n\n", lineCount))
			b.WriteString("```diff\n" + truncateStr(diff, 1024) + "\n```\n\n")
			issues += lineCount
		}
	}

	if p.Check == "vet" || p.Check == "all" {
		b.WriteString("## Correctness (go vet)\n\n")
		// Get the package dir from the first file
		pkgDir := filepath.Dir(matches[0])
		cmd := winhide.CommandContext(ctx, "go", "vet", "./"+pkgDir+"/...")
		out, err := cmd.CombinedOutput()
		if err != nil {
			b.WriteString("❌ `go vet` found issues:\n\n")
			b.WriteString("```\n" + truncateStr(string(out), 1024) + "\n```\n\n")
			issues++
		} else {
			b.WriteString("✅ `go vet` passed\n\n")
		}
	}

	if p.Check == "naming" || p.Check == "all" {
		// Read all files once and reuse across checks
		files := make(map[string]string, len(matches))
		for _, f := range matches {
			data, err := os.ReadFile(f)
			if err == nil {
				files[f] = string(data)
			}
		}

		b.WriteString("## Naming & Style\n\n")
		namingIssues := checkNaming(files)
		if len(namingIssues) > 0 {
			for _, ni := range namingIssues {
				b.WriteString(fmt.Sprintf("⚠️  %s\n", ni))
				issues++
			}
			b.WriteString("\n")
		} else {
			b.WriteString("✅ No naming issues detected\n\n")
		}

		b.WriteString("## Error Handling\n\n")
		errIssues := checkErrorPatterns(files)
		if len(errIssues) > 0 {
			for _, ei := range errIssues {
				b.WriteString(fmt.Sprintf("⚠️  %s\n", ei))
				issues++
			}
			b.WriteString("\n")
		} else {
			b.WriteString("✅ Error handling looks good\n\n")
		}

		b.WriteString("## Import Style\n\n")
		importIssues := checkImports(files)
		if len(importIssues) > 0 {
			for _, ii := range importIssues {
				b.WriteString(fmt.Sprintf("⚠️  %s\n", ii))
				issues++
			}
			b.WriteString("\n")
		} else {
			b.WriteString("✅ Import grouping looks good\n\n")
		}
	}

	if issues == 0 {
		b.WriteString("## ✅ All checks passed\n")
	} else {
		b.WriteString(fmt.Sprintf("## Summary: %d style issue(s) found\n", issues))
		b.WriteString(fmt.Sprintf("\nRun `gofmt -w %s` to fix formatting.\n", matches[0]))
	}

	return b.String(), nil
}

func checkNaming(files map[string]string) []string {
	var issues []string
	for f, content := range files {
		lines := strings.Split(content, "\n")

		for i, line := range lines {
			lineNum := i + 1
			trimmed := strings.TrimSpace(line)

			// Check exported function without comment
			if strings.HasPrefix(trimmed, "func ") && len(trimmed) > 6 {
				rest := trimmed[5:]
				// Exported function detection
				if len(rest) > 0 && rest[0] >= 'A' && rest[0] <= 'Z' {
					// Check if previous line has a doc comment
					if i == 0 || !strings.HasPrefix(strings.TrimSpace(lines[i-1]), "//") {
						issues = append(issues, fmt.Sprintf("%s:%d — exported function lacks doc comment", f, lineNum))
					}
				}
			}

			// Check exported type without comment
			if strings.HasPrefix(trimmed, "type ") && len(trimmed) > 6 {
				rest := trimmed[5:]
				if len(rest) > 0 && rest[0] >= 'A' && rest[0] <= 'Z' {
					if i == 0 || !strings.HasPrefix(strings.TrimSpace(lines[i-1]), "//") {
						issues = append(issues, fmt.Sprintf("%s:%d — exported type lacks doc comment", f, lineNum))
					}
				}
			}
		}
	}
	return issues
}

func checkErrorPatterns(files map[string]string) []string {
	var issues []string
	for f, content := range files {
		lines := strings.Split(content, "\n")

		for i, line := range lines {
			trimmed := strings.TrimSpace(line)

			// _ = ignoring error
			if strings.Contains(trimmed, "_ = ") && strings.Contains(trimmed, "(") {
				issues = append(issues, fmt.Sprintf("%s:%d — `_ =` discarding potential error return", f, i+1))
			}

			// TODO without context
			if strings.Contains(trimmed, "// TODO") && !strings.Contains(trimmed, "// TODO(") && !strings.Contains(trimmed, "// TODO:") {
				issues = append(issues, fmt.Sprintf("%s:%d — TODO without explanation", f, i+1))
			}
		}
	}
	return issues
}

func checkImports(files map[string]string) []string {
	var issues []string
	for f, content := range files {
		// Check if imports are grouped (std → external → internal)
		if strings.Contains(content, "import (") || strings.Contains(content, "import(") {
			importStart := strings.Index(content, "import (")
			if importStart < 0 {
				importStart = strings.Index(content, "import(")
			}
			if importStart >= 0 {
				importEnd := strings.Index(content[importStart:], ")")
				if importEnd >= 0 {
					importBlock := content[importStart : importStart+importEnd+1]
					importLines := strings.Split(importBlock, "\n")

					// Check for 3-group separation (std | external | internal)
					groups := 0
					hasBlank := false
					for _, l := range importLines {
						trimmed := strings.TrimSpace(l)
						if trimmed == "" {
							if hasBlank {
								groups++
							}
							hasBlank = true
						}
					}
					if groups < 2 {
						issues = append(issues, fmt.Sprintf("%s — imports should be grouped (std | external | internal)", f))
					}
				}
			}
		}
	}
	return issues
}
