// Package v2 — world-class static analysis platform. Three layers:
//  1. External tool adapters (golangci-lint/semgrep/ruff/shellcheck/eslint)
//  2. Self-built semantic analyzers (nil-path/race/leak/SQLi/crypto)
//  3. Architecture scanners (god-pkg/cycle/layer/gap)
//
// Single `ok-verify` command covers Go/Python/JS-TS/Rust/Shell/mixed repos.
// Output: terminal table or structured JSON with severity grading + fix patches.
package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/NB-Agent/ok/internal/winhide"
)

// ─── Types ──────────────────────────────────────────────────────────────

type Severity int

const (
	SevInfo     Severity = 0
	SevLow      Severity = 1
	SevMedium   Severity = 2
	SevHigh     Severity = 3
	SevCritical Severity = 4
)

func (s Severity) String() string {
	switch s {
	case SevCritical:
		return "CRITICAL"
	case SevHigh:
		return "HIGH"
	case SevMedium:
		return "MEDIUM"
	case SevLow:
		return "LOW"
	default:
		return "INFO"
	}
}

type Finding struct {
	Analyzer string   `json:"analyzer"`
	Layer    string   `json:"layer"`
	Severity Severity `json:"severity"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Column   int      `json:"column"`
	Message  string   `json:"message"`
	Category string   `json:"category,omitempty"`
	Fix      string   `json:"fix,omitempty"`
	Rule     string   `json:"rule,omitempty"`
}

type Analyzer interface {
	Name() string
	Layer() string
	Languages() []string
	Run(ctx context.Context, root string) ([]Finding, error)
}

type Summary struct {
	Total      int            `json:"total"`
	Critical   int            `json:"critical"`
	High       int            `json:"high"`
	Medium     int            `json:"medium"`
	Low        int            `json:"low"`
	Info       int            `json:"info"`
	ByLayer    map[string]int `json:"by_layer"`
	ByCategory map[string]int `json:"by_category"`
}

type Report struct {
	Findings []Finding `json:"findings"`
	Summary  Summary   `json:"summary"`
}

// ─── Registry ────────────────────────────────────────────────────────────

type Registry struct{ analyzers []Analyzer }

func NewRegistry() *Registry        { return &Registry{} }
func (r *Registry) Add(a Analyzer)  { r.analyzers = append(r.analyzers, a) }
func (r *Registry) All() []Analyzer { return r.analyzers }
func (r *Registry) RunAll(ctx context.Context, root string, lang string) []Finding {
	return runAll(ctx, r.analyzers, root, lang)
}

// ─── Runner ──────────────────────────────────────────────────────────────

func runAll(ctx context.Context, analyzers []Analyzer, root, filterLang string) []Finding {
	var mu sync.Mutex
	var all []Finding
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)

	for _, a := range analyzers {
		if filterLang != "" && filterLang != "*" && !langMatch(a.Languages(), filterLang) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(an Analyzer) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					all = append(all, Finding{Analyzer: an.Name(), Severity: SevInfo, Message: fmt.Sprintf("panic: %v", r)})
					mu.Unlock()
				}
			}()
			ff, err := an.Run(ctx, root)
			if err != nil {
				mu.Lock()
				all = append(all, Finding{Analyzer: an.Name(), Severity: SevInfo, Message: fmt.Sprintf("error: %v", err)})
				mu.Unlock()
				return
			}
			mu.Lock()
			all = append(all, ff...)
			mu.Unlock()
		}(a)
	}
	wg.Wait()

	sort.Slice(all, func(i, j int) bool {
		if all[i].Severity != all[j].Severity {
			return all[i].Severity > all[j].Severity
		}
		if all[i].File != all[j].File {
			return all[i].File < all[j].File
		}
		return all[i].Line < all[j].Line
	})
	return all
}

func langMatch(langs []string, target string) bool {
	for _, l := range langs {
		if l == "*" || l == target {
			return true
		}
	}
	return false
}

// ─── Reporter ────────────────────────────────────────────────────────────

func BuildSummary(ff []Finding) Summary {
	s := Summary{ByLayer: map[string]int{}, ByCategory: map[string]int{}}
	for _, f := range ff {
		s.Total++
		switch f.Severity {
		case SevCritical:
			s.Critical++
		case SevHigh:
			s.High++
		case SevMedium:
			s.Medium++
		case SevLow:
			s.Low++
		default:
			s.Info++
		}
		s.ByLayer[f.Layer]++
		if f.Category != "" {
			s.ByCategory[f.Category]++
		}
	}
	return s
}

func (r *Report) JSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

func (r *Report) Terminal() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# ok-verify v2\n\n%d findings (%dC/%dH/%dM/%dL/%dI)\n\n",
		r.Summary.Total, r.Summary.Critical, r.Summary.High, r.Summary.Medium, r.Summary.Low, r.Summary.Info))
	b.WriteString("## By layer\n")
	for layer, count := range r.Summary.ByLayer {
		b.WriteString(fmt.Sprintf("- %s: %d\n", layer, count))
	}
	if len(r.Summary.ByCategory) > 0 {
		b.WriteString("\n## By category\n")
		for cat, count := range r.Summary.ByCategory {
			b.WriteString(fmt.Sprintf("- %s: %d\n", cat, count))
		}
	}
	for _, sev := range []Severity{SevCritical, SevHigh, SevMedium, SevLow, SevInfo} {
		group := filterBySev(r.Findings, sev)
		b.WriteString(formatSevGroup(group, sev))
	}
	return b.String()
}

func filterBySev(ff []Finding, s Severity) []Finding {
	var out []Finding
	for _, f := range ff {
		if f.Severity == s {
			out = append(out, f)
		}
	}
	return out
}

func formatSevGroup(ff []Finding, sev Severity) string {
	if len(ff) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n## %s (%d)\n\n", sev.String(), len(ff))
	for _, f := range ff {
		fmt.Fprintf(&b, "%s:%d:%d: [%s] %s\n", f.File, f.Line, f.Column, f.Analyzer, f.Message)
		if f.Fix != "" {
			for _, line := range strings.Split(f.Fix, "\n") {
				fmt.Fprintf(&b, "  fix: %s\n", line)
			}
		}
	}
	return b.String()
}

// ─── Shared Helpers ───────────────────────────────────────────────────────

func which(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func runCmd(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := winhide.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func contains(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func containsAny(list []string, targets []string) bool {
	for _, s := range list {
		for _, t := range targets {
			if s == t {
				return true
			}
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

var readFile = func(path string) ([]byte, error) { return os.ReadFile(path) }

func isStdLib(pkg string) bool {
	if strings.Contains(pkg, ".") {
		return false
	}
	return !strings.ContainsRune(pkg, '.') && !strings.Contains(pkg, "/vendor/")
}

func moduleName(root string) string {
	cmd := winhide.Command("go", "list", "-m")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
