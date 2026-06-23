// Package command loads custom slash commands from Markdown files. A command is
// a prompt template: invoking /name substitutes the arguments into the body and
// sends the result as a chat turn. Loading is pure and dependency-free — a small
// "key: value" frontmatter parser keeps OK's single-(TOML)-dependency promise
// rather than pulling in a YAML library.
package command

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/NB-Agent/ok/internal/frontmatter"
)

// Command is a custom slash command loaded from a .md file.
type Command struct {
	Name        string // "review" or "git:commit", derived from the file path
	Description string // from frontmatter
	ArgHint     string // from frontmatter (argument-hint)
	Body        string // template with $ARGUMENTS / $1..$N / $$
	Source      string // originating file path, for diagnostics
}

// substRe matches the substitution tokens recognised in a command body.
var substRe = regexp.MustCompile(`\$(\$|ARGUMENTS|[0-9]+)`)

// Render substitutes args into the command body: $ARGUMENTS is all args joined
// by spaces, $1..$N are positional (empty when absent), and $$ is a literal $.
func (c Command) Render(args []string) string {
	return substRe.ReplaceAllStringFunc(c.Body, func(m string) string {
		switch tok := m[1:]; tok {
		case "$":
			return "$"
		case "ARGUMENTS":
			return strings.Join(args, " ")
		default:
			n, _ := strconv.Atoi(tok) // regex guarantees digits
			if n >= 1 && n <= len(args) {
				return args[n-1]
			}
			return ""
		}
	})
}

// Load reads every *.md command file under each dir, in order, so a later dir
// overrides an earlier one on a name clash (pass the user dir first, project
// dir last). Missing dirs are skipped. Individual file failures are collected
// into the returned error but don't prevent the others from loading. The result
// is sorted by name.
func Load(dirs ...string) ([]Command, error) {
	byName := map[string]Command{}
	var errs []string
	for _, dir := range dirs {
		root, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		// A symlink-following walk (filepath.WalkDir does not follow links), so a
		// symlinked command directory or a symlinked <name>.md is picked up like a
		// real one. visited (keyed by resolved path) guards against symlink cycles.
		visited := map[string]bool{}
		if real, err := filepath.EvalSymlinks(root); err == nil {
			visited[real] = true
		} else {
			visited[root] = true
		}
		walkCommands(root, root, visited, func(path string) {
			c, perr := parseFile(root, path)
			if perr != nil {
				errs = append(errs, perr.Error())
				return
			}
			byName[c.Name] = c
		})
	}
	cmds := make([]Command, 0, len(byName))
	for _, c := range byName {
		cmds = append(cmds, c)
	}
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	if len(errs) > 0 {
		return cmds, fmt.Errorf("command load: %s", strings.Join(errs, "; "))
	}
	return cmds, nil
}

// walkCommands recursively visits dir, following symlinks, and calls fn with the
// path of every *.md file (including symlinked files and files under symlinked
// directories). visited (resolved-path set) prevents infinite recursion through
// a symlink cycle. Unreadable directories are skipped, never fatal.
func walkCommands(root, dir string, visited map[string]bool, fn func(path string)) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		isDir := e.IsDir()
		isFile := e.Type().IsRegular()
		isSymlink := e.Type()&os.ModeSymlink != 0
		if isSymlink {
			// Resolve the symlink target and verify it stays under root.
			real, rerr := filepath.EvalSymlinks(full)
			if rerr != nil {
				continue // broken link
			}
			info, serr := os.Stat(real)
			if serr != nil {
				continue
			}
			isDir = info.IsDir()
			isFile = info.Mode().IsRegular()
			// Only allow symlinks that resolve within the command root directory.
			rel, rerr := filepath.Rel(root, real)
			if rerr != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) && !strings.HasPrefix(real, root) {
				continue // symlink escapes root
			}
		}
		switch {
		case isDir:
			real, rerr := filepath.EvalSymlinks(full)
			if rerr != nil {
				real = full
			}
			if visited[real] {
				continue
			}
			visited[real] = true
			walkCommands(root, full, visited, fn)
		case isFile && strings.EqualFold(filepath.Ext(e.Name()), ".md"):
			fn(full)
		default: // ignore non-md files
		}
	}
}

// parseFile reads one command file and derives its name from the path relative
// to root: drop the .md suffix and turn subdirectories into ":" namespaces
// (git/commit.md → git:commit).
func parseFile(root, path string) (Command, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Command{}, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	name := strings.ReplaceAll(trimExt(filepath.ToSlash(rel), ".md"), "/", ":")

	content := frontmatter.Normalize(b)
	fm, body := frontmatter.Parse(content)
	return Command{
		Name:        name,
		Description: fm["description"],
		ArgHint:     fm["argument-hint"],
		Body:        strings.TrimSpace(body),
		Source:      path,
	}, nil
}

// splitFrontmatter separates an optional leading ---fenced block of simple
// "key: value" lines from the body. Returns the parsed keys (lowercased) and the
// remaining body. With no opening/closing fence, the whole input is the body.
// trimExt removes suffix from path case-insensitively — counterpart to the
// EqualFold check in the directory walk.
func trimExt(path, ext string) string {
	if strings.HasSuffix(strings.ToLower(path), strings.ToLower(ext)) {
		return path[:len(path)-len(ext)]
	}
	return path
}
