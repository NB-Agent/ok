package builtin

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/NB-Agent/ok/internal/sandbox"
	"github.com/NB-Agent/ok/internal/tool"
)

// Workspace binds all file tools to a project directory. When Dir is non-empty,
// relative paths are resolved within it and writes outside it are confined.
// AllowWrite lists additional directories file-writer tools may modify (e.g.
// /tmp for temporary output).
type Workspace struct {
	Dir        string
	WriteRoots []string
	Bash       sandbox.Spec
}

// Tools returns workspace-scoped built-in tools: writers confined to
// WriteRoots (falling back to Dir), readers open, and bash bound to the
// sandbox spec. When enabled is non-empty, only those named tools are
// returned; nil/empty means all.
func (ws Workspace) Tools(enabled ...string) []tool.Tool {
	// Determine write roots: WriteRoots wins, fall back to Dir.
	writeRoots := ws.WriteRoots
	if len(writeRoots) == 0 && ws.Dir != "" {
		writeRoots = []string{ws.Dir}
	}

	// Build confined + workspace-scoped tool instances.
	all := make([]tool.Tool, 0, 40)
	toolMap := make(map[string]tool.Tool)

	// Readers with workDir set for path resolution.
	for _, t := range ConfineReaders(writeRoots) {
		switch v := t.(type) {
		case readFile:
			v.workDir = ws.Dir
			toolMap[v.Name()] = v
		case listDir:
			v.workDir = ws.Dir
			toolMap[v.Name()] = v
		case globTool:
			v.workDir = ws.Dir
			toolMap[v.Name()] = v
		case grepTool:
			v.workDir = ws.Dir
			toolMap[v.Name()] = v
		default:
			toolMap[t.Name()] = t
		}
	}

	// Writers with workDir set.
	for _, t := range ConfineWriters(writeRoots) {
		switch v := t.(type) {
		case writeFile:
			v.workDir = ws.Dir
			toolMap[v.Name()] = v
		case editFile:
			v.workDir = ws.Dir
			toolMap[v.Name()] = v
		case multiEdit:
			v.workDir = ws.Dir
			toolMap[v.Name()] = v
		default:
			toolMap[t.Name()] = t
		}
	}

	// Bash with sandbox spec.
	toolMap["bash"] = ConfineBash(ws.Bash)

	// All other built-ins (non-file, non-bash).
	for _, t := range tool.Builtins() {
		name := t.Name()
		if _, exists := toolMap[name]; !exists {
			switch name {
			case "bash", "write_file", "edit_file", "multi_edit",
				"read_file", "ls", "glob", "grep":
				continue
			default:
				toolMap[name] = t
			}
		}
	}

	if len(enabled) == 0 {
		for _, t := range tool.Builtins() {
			if tt, ok := toolMap[t.Name()]; ok {
				all = append(all, tt)
			}
		}
		return all
	}

	enabledSet := make(map[string]bool, len(enabled))
	for _, n := range enabled {
		enabledSet[n] = true
	}
	for _, name := range enabled {
		if t, ok := toolMap[name]; ok && enabledSet[name] {
			all = append(all, t)
		}
	}
	return all
}

// resolveIn returns the absolute path for a path relative to workDir. If
// workDir is empty the path is returned unchanged; if the path is already
// absolute it is returned verbatim; otherwise workDir and p are joined and
// cleaned. The caller (the confiner) is responsible for enforcing boundary
// checks — this function only resolves.
func resolveIn(workDir, p string) string {
	if workDir == "" {
		return p
	}
	if p == "" || p == "." {
		return workDir
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workDir, p)
}

// isWriteAllowed checks whether the given absolute path falls within the
// workspace directory or any of the allowed write directories.
//
//lint:ignore U1000 kept for future workspace-check use
func isWriteAllowed(absPath, workDir string, allowWrite []string) bool {
	if workDir != "" && isWithin(absPath, workDir) {
		return true
	}
	for _, d := range allowWrite {
		if d != "" && isWithin(absPath, d) {
			return true
		}
	}
	return false
}

// isWithin checks whether child is within or equal to parent, after resolving
// symlinks and cleaning both paths.
//
//lint:ignore U1000 kept for future workspace-check use
func isWithin(child, parent string) bool {
	c, err := filepath.EvalSymlinks(child)
	if err != nil {
		c = filepath.Clean(child)
	}
	p, err := filepath.EvalSymlinks(parent)
	if err != nil {
		p = filepath.Clean(parent)
	}
	return strings.HasPrefix(c, p+string(os.PathSeparator)) || c == p
}
