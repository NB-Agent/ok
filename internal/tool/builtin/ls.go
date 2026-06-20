package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(listDir{}) }

// listDir lists a directory. roots, when non-empty, confines reads to the
// workspace (see confineRead). workDir, when non-empty, is the directory a
// relative path resolves against (see resolveIn).
type listDir struct {
	roots   []string
	workDir string
}

func (listDir) Name() string { return "ls" }

func (listDir) Description() string {
	return "List directory entries. Directories shown with trailing slash; files show byte size."
}

func (listDir) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"path":{"type":"string"}},"type":"object"}`)
}

func (listDir) ReadOnly() bool { return true }

func (l listDir) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	p := struct {
		Path string `json:"path"`
	}{Path: "."}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	if p.Path == "" {
		p.Path = "."
	}
	p.Path = resolveIn(l.workDir, p.Path)
	if err := confineRead(l.roots, p.Path); err != nil {
		return "", err
	}
	resolved, err := realPath(p.Path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", p.Path, err)
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		return "", fmt.Errorf("ls %s: %w", resolved, err)
	}

	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(&b, "%s/\n", e.Name())
			continue
		}
		size := int64(-1)
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		fmt.Fprintf(&b, "%s\t%d\n", e.Name(), size)
	}
	if b.Len() == 0 {
		return "(empty directory)", nil
	}
	return b.String(), nil
}
