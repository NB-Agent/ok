package builtin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/tool"
)

func init() { tool.RegisterBuiltin(grepTool{}) }

// grepTool searches files for a regex pattern.
type grepTool struct {
	roots   []string
	workDir string
}

const grepMaxMatches = 200

// grepRegexCache caches compiled regex patterns to avoid recompilation on
// repeated grep calls. Capped at 64 entries; LRU by map iteration order.
type grepRegexCache struct {
	mu   sync.Mutex
	m    map[string]*regexp.Regexp
	keys []string // insertion order for simple cap eviction
}

func (c *grepRegexCache) get(pattern string) *regexp.Regexp {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		return nil
	}
	return c.m[pattern]
}

func (c *grepRegexCache) set(pattern string, re *regexp.Regexp) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[string]*regexp.Regexp, 64)
	}
	if _, ok := c.m[pattern]; ok {
		return
	}
	if len(c.keys) >= 64 {
		delete(c.m, c.keys[0])
		c.keys = c.keys[1:]
	}
	c.m[pattern] = re
	c.keys = append(c.keys, pattern)
}

var grepRegexCacheInst grepRegexCache

func (grepTool) Name() string         { return "grep" }
func (grepTool) ReadOnly() bool       { return true }
func (grepTool) CostCategory() string { return "slow" }

func (grepTool) Description() string {
	return "Search for a regex in a file or directory. Returns path:line:text, capped at 200 matches."
}

func (grepTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"path":{"type":"string"},"pattern":{"type":"string"}},"required":["pattern"],"type":"object"}`)
}

func (g grepTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("grep: %w", err)
	}
	if p.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if p.Path == "" {
		p.Path = "."
	}
	p.Path = resolveIn(g.workDir, p.Path)
	if err := confineRead(g.roots, p.Path); err != nil {
		return "", err
	}
	resolved, err := realPath(p.Path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", p.Path, err)
	}
	re := grepRegexCacheInst.get(p.Pattern)
	if re == nil {
		re, err = regexp.Compile(p.Pattern)
		if err != nil {
			return "", fmt.Errorf("invalid pattern: %w", err)
		}
		grepRegexCacheInst.set(p.Pattern, re)
	}

	var out []string
	truncated := false

	// searchFile returns io.EOF as a sentinel once the cap is reached.
	searchFile := func(file string) error {
		f, err := os.Open(file)
		if err != nil {
			return nil // skip unreadable files
		}
		defer log.Close("grep file", f)

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		ln := 0
		for sc.Scan() {
			ln++
			line := sc.Text()
			if re.MatchString(line) {
				out = append(out, file+":"+strconv.Itoa(ln)+":"+strings.TrimRight(line, "\r\n"))
				if len(out) >= grepMaxMatches {
					truncated = true
					return io.EOF
				}
			}
		}
		return sc.Err()
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", resolved, err)
	}

	if info.IsDir() {
		if err := filepath.WalkDir(resolved, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor" ||
					d.Name() == ".next" || d.Name() == "build" || d.Name() == "dist" ||
					d.Name() == "__pycache__" || d.Name() == ".pytest_cache" ||
					d.Name() == ".venv" || d.Name() == "env" || d.Name() == "venv" {
					return filepath.SkipDir
				}
				return nil
			}
			if searchFile(path) == io.EOF {
				return filepath.SkipAll
			}
			return nil
		}); err != nil {
			// Best-effort walk; file errors reported in results.
		}
	} else {
		if err := searchFile(resolved); err != nil {
			// Scanner error (e.g. line too long); surface to caller.
			return "", fmt.Errorf("grep %s: %w", resolved, err)
		}
	}

	if len(out) == 0 {
		return "(no matches)", nil
	}
	res := strings.Join(out, "\n")
	if truncated {
		res += fmt.Sprintf("\n... (truncated at %d matches)", grepMaxMatches)
	}
	return res, nil
}
