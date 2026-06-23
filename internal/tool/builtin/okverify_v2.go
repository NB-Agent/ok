package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/NB-Agent/ok/internal/tool"
	v2 "github.com/NB-Agent/ok/internal/verification/v2"
)

func init() { tool.RegisterBuiltin(okVerifyV2{}) }

// okVerifyV2 delegates to the v2 analysis platform: 5 external adapters
// (golangci-lint/semgrep/ruff/eslint/shellcheck) + 6 self-built semantic
// analyzers (sqli/crypto/race/leak/godpkg/testgap).  Language auto-detection
// means it works on Go, Python, JS/TS, Rust, and shell projects.
type okVerifyV2 struct{}

func (okVerifyV2) Name() string   { return "ok-verify" }
func (okVerifyV2) ReadOnly() bool { return true }

func (okVerifyV2) Description() string {
	return "14 static analyzers across Go/Python/JS/TS/Shell/Rust. Zero-token deep audit. Use instead of task() sub-agents."
}

func (okVerifyV2) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"format":{"enum":["terminal","json"],"type":"string"},"scope":{"type":"string"}},"type":"object"}`)
}

func (okVerifyV2) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Scope  string `json:"scope"`
		Format string `json:"format"`
	}
	json.Unmarshal(args, &p)
	if p.Scope == "" {
		p.Scope = "."
	}

	root := p.Scope
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		root = "."
	}

	report, err := v2.Scan(ctx, root)
	if err != nil {
		return "", fmt.Errorf("v2 scan: %w", err)
	}

	switch p.Format {
	case "json":
		return report.JSON(), nil
	default:
		return report.Terminal(), nil
	}
}
