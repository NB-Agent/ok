package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
	"github.com/NB-Agent/ok/internal/winhide"
)

func init() { tool.RegisterBuiltin(vulnCheck{}) }

// vulnCheck runs govulncheck on the project and returns known vulnerabilities.
// Falls back gracefully when govulncheck is not installed.
type vulnCheck struct{}

func (vulnCheck) Name() string { return "vuln-check" }

func (vulnCheck) Description() string {
	return "Scan Go project for known vulnerabilities using govulncheck. Returns severity, module, and fix version for each finding."
}

func (vulnCheck) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"scan":{"enum":["source","binary"],"type":"string"},"target":{"type":"string"}},"type":"object"}`)
}

func (vulnCheck) ReadOnly() bool { return true }

func (vulnCheck) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Target string `json:"target"`
		Scan   string `json:"scan"`
	}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	if p.Target == "" {
		p.Target = "./..."
	}

	var b strings.Builder
	b.WriteString("# Vulnerability Scan\n\n")
	b.WriteString(fmt.Sprintf("Target: `%s`\n\n", p.Target))

	// Check if govulncheck is available
	if _, err := exec.LookPath("govulncheck"); err != nil {
		// Try go install approach
		b.WriteString("## ⚠️ govulncheck not installed\n\n")
		b.WriteString("Install: `go install golang.org/x/vuln/cmd/govulncheck@latest`\n\n")

		// Try `go version -m` on the binary as fallback
		b.WriteString("## Binary analysis (go version -m)\n\n")
		cmd := winhide.CommandContext(ctx, "go", "version", "-m", "ok.exe")
		out, err := cmd.CombinedOutput()
		if err == nil {
			b.WriteString("```\n" + truncateStr(string(out), 1000) + "\n```\n")
		} else {
			b.WriteString("(binary not found or not built)\n")
		}
		return b.String(), nil
	}

	// Run govulncheck
	args_ := []string{p.Target}
	if p.Scan == "binary" {
		args_ = append(args_, "-mode=binary")
	}
	cmd := winhide.CommandContext(ctx, "govulncheck", args_...)
	out, err := cmd.CombinedOutput()
	output := string(out)

	if err != nil && !strings.Contains(output, "Found") {
		b.WriteString("## ⚠️ Scan error\n\n")
		b.WriteString("```\n" + truncateStr(output, 500) + "\n```\n")
		b.WriteString("\n(Run `go install golang.org/x/vuln/cmd/govulncheck@latest` to get the latest vulnerability database)\n")
		return b.String(), nil
	}

	// Parse and summarize
	if strings.Contains(output, "No vulnerabilities found") || !strings.Contains(output, "Vulnerability #") {
		b.WriteString("## ✅ No vulnerabilities found\n")
		return b.String(), nil
	}

	b.WriteString("## 🔴 Vulnerabilities Found\n\n")

	// Extract each vulnerability block
	lines := strings.Split(output, "\n")
	var vulnBlocks []string
	var currentBlock strings.Builder
	inVuln := false

	for _, line := range lines {
		if strings.HasPrefix(line, "Vulnerability #") {
			if inVuln && currentBlock.Len() > 0 {
				vulnBlocks = append(vulnBlocks, currentBlock.String())
				currentBlock.Reset()
			}
			inVuln = true
			currentBlock.WriteString(line + "\n")
		} else if inVuln {
			if strings.TrimSpace(line) == "" && currentBlock.Len() > 0 {
				vulnBlocks = append(vulnBlocks, currentBlock.String())
				currentBlock.Reset()
				inVuln = false
			} else {
				currentBlock.WriteString(line + "\n")
			}
		}
	}
	if currentBlock.Len() > 0 {
		vulnBlocks = append(vulnBlocks, currentBlock.String())
	}

	for i, block := range vulnBlocks {
		b.WriteString(fmt.Sprintf("### %d\n", i+1))
		// Extract key fields from the block
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// Highlight severity and fix info
			if strings.Contains(line, "Found in:") || strings.Contains(line, "Fixed in:") ||
				strings.Contains(line, "More info:") || strings.Contains(line, "Call stacks:") {
				b.WriteString(fmt.Sprintf("- %s\n", line))
			} else {
				b.WriteString(line + "\n")
			}
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}
