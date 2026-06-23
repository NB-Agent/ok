package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(globTool{}) }

// globTool matches files by pattern. roots, when non-empty, confines reads to
// the workspace (see confineRead). workDir, when non-empty, is the directory a
// relative pattern resolves against (see resolveIn).
type globTool struct {
	roots   []string
	workDir string
}

func (globTool) Name() string { return "glob" }

func (globTool) Description() string {
	return "Find files matching a glob pattern (e.g. \"*.go\"). Supports * ? [ ]; use bash find for recursive ** matching."
}

func (globTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"pattern":{"type":"string"}},"required":["pattern"],"type":"object"}`)
}

func (globTool) ReadOnly() bool { return true }

// patternBase returns the longest non-wildcard prefix of a glob pattern for
// path-boundary checks. For "/x/*.go" it returns "/x"; for "*.go" it returns ".".
func patternBase(p string) string {
	dir := filepath.Dir(p)
	// filepath.Dir yields "." for patterns without a separator — use the
	// current directory via "" so confineRead sees the default path.
	if dir == "." && filepath.Base(p) == p {
		return p // relative glob with no dir component, e.g. "*.go"
	}
	return dir
}

func (g globTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	p.Pattern = resolveIn(g.workDir, p.Pattern)
	// Resolve the pattern's base directory for confineRead so absolute
	// patterns like /etc/* are caught even before Glob runs.
	if err := confineRead(g.roots, patternBase(p.Pattern)); err != nil {
		return "", err
	}

	matches, err := filepath.Glob(p.Pattern)
	if err != nil {
		return "", fmt.Errorf("glob %q: %w", p.Pattern, err)
	}
	if len(matches) == 0 {
		return "(no matches)", nil
	}
	return strings.Join(matches, "\n"), nil
}
