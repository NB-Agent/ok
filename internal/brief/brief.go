// Package brief generates a concise project overview (OK-BRIEF.md) that gives
// the AI agent immediate context at session start. Inspired by Stacklit
// (github.com/glincker/stacklit): ~250 tokens of structure knowledge replaces
// 50k+ tokens of initial exploration.
//
// The brief is regenerated on codegraph init and can be manually triggered
// with `codegraph brief`. It is loaded into the agent's system prompt at
// session start via the memory system.
package brief

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileName is the name of the generated brief file.
const FileName = "OK-BRIEF.md"

// Generate creates a project brief in the given directory. It scans the
// directory structure, identifies key files (go.mod, Makefile, etc.), and
// writes a concise markdown summary to OK-BRIEF.md.
//
// The brief is designed to be ~200-400 tokens — small enough to load into
// every session without meaningful cost, but informative enough to orient
// the agent immediately.
func Generate(dir string) error {
	content := buildBrief(dir)
	path := filepath.Join(dir, FileName)
	return os.WriteFile(path, []byte(content), 0644)
}

// Content returns the brief as a string without writing to disk.
func Content(dir string) string {
	return buildBrief(dir)
}

func buildBrief(dir string) string {
	var b strings.Builder
	b.WriteString("# Project Brief\n\n")
	b.WriteString("> Auto-generated — loaded into every session for instant orientation.\n\n")

	// Project identity
	b.WriteString("## Identity\n\n")
	if name := projectName(dir); name != "" {
		b.WriteString(fmt.Sprintf("- **Project**: %s\n", name))
	}
	if lang := detectLanguage(dir); lang != "" {
		b.WriteString(fmt.Sprintf("- **Language**: %s\n", lang))
	}
	if build := detectBuild(dir); build != "" {
		b.WriteString(fmt.Sprintf("- **Build**: %s\n", build))
	}
	b.WriteString("\n")

	// Structure overview
	b.WriteString("## Structure\n\n")
	topDirs := listTopDirs(dir)
	if len(topDirs) > 0 {
		for _, d := range topDirs {
			b.WriteString(fmt.Sprintf("- `%s/`\n", d))
		}
		b.WriteString("\n")
	}

	// Key files
	b.WriteString("## Key Files\n\n")
	keyFiles := findKeyFiles(dir)
	if len(keyFiles) > 0 {
		for _, f := range keyFiles {
			b.WriteString(fmt.Sprintf("- `%s`\n", f))
		}
		b.WriteString("\n")
	}

	// Entry points
	b.WriteString("## Entry Points\n\n")
	entries := findEntryPoints(dir)
	if len(entries) > 0 {
		for _, e := range entries {
			b.WriteString(fmt.Sprintf("- `%s` — %s\n", e.path, e.desc))
		}
	} else {
		b.WriteString("_(auto-detect unavailable — check build system)_\n")
	}
	b.WriteString("\n")

	// Dependencies (from go.mod / package.json / requirements.txt)
	b.WriteString("## Dependencies\n\n")
	deps := listDeps(dir)
	if len(deps) > 0 {
		for _, d := range deps {
			b.WriteString(fmt.Sprintf("- %s\n", d))
		}
	} else {
		b.WriteString("_(none detected)_\n")
	}

	return b.String()
}

// projectName extracts the project name from go.mod / package.json.
func projectName(dir string) string {
	if name := moduleName(dir); name != "" {
		return name
	}
	if name := npmName(dir); name != "" {
		return name
	}
	return filepath.Base(dir)
}

func moduleName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func npmName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	// Simple heuristic — look for "name" field.
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "\"name\"") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.Trim(strings.TrimSpace(parts[1]), "\",")
			}
		}
	}
	return ""
}

func detectLanguage(dir string) string {
	exts := map[string]int{}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && (d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != "" {
			exts[ext]++
		}
		if len(exts) > 1000 {
			return filepath.SkipAll // enough data
		}
		return nil
	})
	if len(exts) == 0 {
		return ""
	}

	langMap := map[string]string{
		".go":     "Go",
		".rs":     "Rust",
		".py":     "Python",
		".js":     "JavaScript",
		".ts":     "TypeScript",
		".tsx":    "TypeScript/React",
		".jsx":    "JavaScript/React",
		".java":   "Java",
		".kt":     "Kotlin",
		".swift":  "Swift",
		".c":      "C",
		".cpp":    "C++",
		".h":      "C/C++",
		".rb":     "Ruby",
		".php":    "PHP",
		".cs":     "C#",
		".scala":  "Scala",
		".dart":   "Dart",
		".lua":    "Lua",
		".vue":    "Vue",
		".svelte": "Svelte",
		".mdx":    "MDX",
	}

	counts := map[string]int{}
	for ext, count := range exts {
		if lang, ok := langMap[ext]; ok {
			counts[lang] += count
		}
	}

	top := ""
	topCount := 0
	for lang, count := range counts {
		if count > topCount {
			topCount = count
			top = lang
		}
	}
	if top != "" {
		rest := make([]string, 0)
		for lang, count := range counts {
			if lang != top && count > topCount/5 {
				rest = append(rest, fmt.Sprintf("%s (%d files)", lang, count))
			}
		}
		if len(rest) > 0 {
			return fmt.Sprintf("%s + %s", top, strings.Join(rest, ", "))
		}
	}
	return top
}

func detectBuild(dir string) string {
	builds := []string{}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		builds = append(builds, "go mod")
	}
	if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
		builds = append(builds, "make")
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		builds = append(builds, "npm")
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		builds = append(builds, "cargo")
	}
	if _, err := os.Stat(filepath.Join(dir, "CMakeLists.txt")); err == nil {
		builds = append(builds, "cmake")
	}
	return strings.Join(builds, ", ")
}

func listTopDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var dirs []string
	skip := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"__pycache__": true, ".codegraph": true, ".ok": true,
		"dist": true, "bin": true, "tmp": true, ".github": true,
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if skip[e.Name()] {
			continue
		}
		// Check if directory has non-hidden files (not just .gitkeep)
		sub, _ := os.ReadDir(filepath.Join(dir, e.Name()))
		hasContent := false
		for _, s := range sub {
			if !strings.HasPrefix(s.Name(), ".") {
				hasContent = true
				break
			}
		}
		if hasContent || strings.HasPrefix(e.Name(), "internal") || strings.HasPrefix(e.Name(), "cmd") || strings.HasPrefix(e.Name(), "pkg") {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	if len(dirs) > 15 {
		dirs = dirs[:15]
		dirs = append(dirs, "...")
	}
	return dirs
}

func findKeyFiles(dir string) []string {
	candidates := []string{
		"README.md", "README.zh-CN.md", "CHANGELOG.md", "LICENSE",
		"go.mod", "Makefile", "Dockerfile", ".env.example",
		"ok.toml", "package.json", "Cargo.toml", "SECURITY.md",
	}
	var found []string
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(dir, c)); err == nil {
			found = append(found, c)
		}
	}
	return found
}

type entryPoint struct {
	path string
	desc string
}

func findEntryPoints(dir string) []entryPoint {
	var entries []entryPoint

	// Go: cmd/*/main.go
	if matches, _ := filepath.Glob(filepath.Join(dir, "cmd", "*", "main.go")); len(matches) > 0 {
		for _, m := range matches {
			rel, _ := filepath.Rel(dir, m)
			entries = append(entries, entryPoint{rel, "command entry"})
		}
	}
	// Go: main.go at root
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err == nil {
		entries = append(entries, entryPoint{"main.go", "root entry"})
	}
	// Makefile targets
	if data, err := os.ReadFile(filepath.Join(dir, "Makefile")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "build:") || strings.HasPrefix(line, "run:") || strings.HasPrefix(line, "test:") {
				entries = append(entries, entryPoint{"Makefile", strings.TrimSuffix(line, ":")})
			}
		}
	}

	if len(entries) > 10 {
		entries = entries[:10]
	}
	return entries
}

func listDeps(dir string) []string {
	var deps []string

	// Go
	if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
		inRequire := false
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "require (" {
				inRequire = true
				continue
			}
			if inRequire {
				if line == ")" {
					break
				}
				if line != "" && !strings.HasPrefix(line, "//") {
					deps = append(deps, line)
				}
			}
		}
	}

	if len(deps) > 20 {
		deps = deps[:20]
		deps = append(deps, fmt.Sprintf("... and %d more", len(deps)-20))
	}
	return deps
}
