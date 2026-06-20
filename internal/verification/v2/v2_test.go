package v2

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSeverityString(t *testing.T) {
	tests := []struct {
		s    Severity
		want string
	}{
		{SevCritical, "CRITICAL"},
		{SevHigh, "HIGH"},
		{SevMedium, "MEDIUM"},
		{SevLow, "LOW"},
		{SevInfo, "INFO"},
		{Severity(99), "INFO"},
	}
	for _, tc := range tests {
		got := tc.s.String()
		if got != tc.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tc.s, got, tc.want)
		}
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if len(r.All()) != 0 {
		t.Errorf("expected empty registry, got %d", len(r.All()))
	}

	ma := &mockAnalyzer{name: "test-ana", layer: "test", langs: []string{"go"}}
	r.Add(ma)
	if len(r.All()) != 1 {
		t.Fatalf("expected 1 analyzer, got %d", len(r.All()))
	}

	ff := r.RunAll(context.Background(), ".", "go")
	if len(ff) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(ff))
	}
	if ff[0].Analyzer != "test-ana" {
		t.Errorf("analyzer = %q", ff[0].Analyzer)
	}

	ff = r.RunAll(context.Background(), ".", "python")
	if len(ff) != 0 {
		t.Errorf("expected 0 findings for python, got %d", len(ff))
	}

	r.Add(&mockAnalyzer{name: "wild-ana", layer: "all", langs: []string{"*"}})
	ff = r.RunAll(context.Background(), ".", "rust")
	if len(ff) != 1 {
		t.Fatalf("expected 1 wildcard finding, got %d", len(ff))
	}
}

func TestLangMatch(t *testing.T) {
	tests := []struct {
		langs  []string
		target string
		want   bool
	}{
		{[]string{"go"}, "go", true},
		{[]string{"go", "python"}, "python", true},
		{[]string{"go"}, "python", false},
		{[]string{"*"}, "go", true},
		{[]string{"*"}, "anything", true},
		{nil, "go", false},
	}
	for _, tc := range tests {
		got := langMatch(tc.langs, tc.target)
		if got != tc.want {
			t.Errorf("langMatch(%v, %q) = %v, want %v", tc.langs, tc.target, got, tc.want)
		}
	}
}

func TestBuildSummary(t *testing.T) {
	ff := []Finding{
		{Severity: SevCritical, Layer: "security", Category: "injection"},
		{Severity: SevHigh, Layer: "security", Category: "injection"},
		{Severity: SevMedium, Layer: "style"},
		{Severity: SevLow, Layer: "style"},
		{Severity: SevInfo, Layer: "info"},
		{Severity: SevMedium, Layer: "style"},
		{Severity: SevInfo, Layer: "info"},
	}
	s := BuildSummary(ff)
	if s.Total != 7 {
		t.Errorf("Total = %d, want 7", s.Total)
	}
	if s.Critical != 1 || s.High != 1 || s.Medium != 2 || s.Low != 1 || s.Info != 2 {
		t.Errorf("counts: C=%d H=%d M=%d L=%d I=%d", s.Critical, s.High, s.Medium, s.Low, s.Info)
	}
	if s.ByLayer["security"] != 2 || s.ByLayer["style"] != 3 || s.ByLayer["info"] != 2 {
		t.Errorf("ByLayer: %v", s.ByLayer)
	}
	if s.ByCategory["injection"] != 2 {
		t.Errorf("ByCategory injection = %d, want 2", s.ByCategory["injection"])
	}
}

func TestReportJSON(t *testing.T) {
	ff := []Finding{{Severity: SevInfo, Analyzer: "test", File: "a.go", Line: 1, Message: "test"}}
	r := &Report{Findings: ff, Summary: BuildSummary(ff)}
	j := r.JSON()
	if j == "" {
		t.Fatal("JSON() returned empty")
	}
}

func TestReportTerminal(t *testing.T) {
	ff := []Finding{
		{Severity: SevCritical, Analyzer: "test", File: "a.go", Line: 1, Column: 5, Message: "critical issue"},
		{Severity: SevInfo, Analyzer: "test2", File: "b.go", Line: 2, Message: "info note"},
	}
	r := &Report{Findings: ff, Summary: BuildSummary(ff)}
	term := r.Terminal()
	if !containsStr(term, "CRITICAL") || !containsStr(term, "a.go") || !containsStr(term, "b.go") {
		t.Errorf("terminal missing expected content")
	}
}

func TestFilterBySev(t *testing.T) {
	ff := []Finding{
		{Severity: SevHigh},
		{Severity: SevLow},
		{Severity: SevHigh},
	}
	got := filterBySev(ff, SevHigh)
	if len(got) != 2 {
		t.Errorf("got %d high findings, want 2", len(got))
	}
}

func TestContains(t *testing.T) {
	if !contains([]string{"a", "b", "c"}, "b") {
		t.Error("contains should find b")
	}
	if contains([]string{"a", "b"}, "z") {
		t.Error("contains should not find z")
	}
}

func TestContainsAny(t *testing.T) {
	if !containsAny([]string{"a", "b"}, []string{"b", "c"}) {
		t.Error("containsAny should find b")
	}
	if containsAny([]string{"a"}, []string{"b"}) {
		t.Error("containsAny should not find")
	}
}

func TestIsStdLib(t *testing.T) {
	tests := []struct {
		pkg  string
		want bool
	}{
		{"fmt", true},
		{"net/http", true},
		{"github.com/foo/bar", false},
		{"pkg/vendor/foo", false},
	}
	for _, tc := range tests {
		got := isStdLib(tc.pkg)
		if got != tc.want {
			t.Errorf("isStdLib(%q) = %v, want %v", tc.pkg, got, tc.want)
		}
	}
}

func TestTruncateString(t *testing.T) {
	s := "hello world"
	if got := truncateString(s, 5); got != "hello..." {
		t.Errorf("truncate(5) = %q", got)
	}
	if got := truncateString(s, 20); got != s {
		t.Errorf("truncate(20) = %q", got)
	}
}

func TestFormatSevGroup(t *testing.T) {
	ff := []Finding{
		{Severity: SevHigh, Analyzer: "a", File: "f.go", Line: 1, Column: 2, Message: "m", Fix: "replace x with y"},
	}
	got := formatSevGroup(ff, SevHigh)
	if !containsStr(got, "f.go:1:2") || !containsStr(got, "replace x with y") {
		t.Errorf("formatSevGroup missing content: %q", got)
	}
}

func TestFormatSevGroupEmpty(t *testing.T) {
	got := formatSevGroup(nil, SevHigh)
	if got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
}

func TestNewDefaultRegistry(t *testing.T) {
	r := NewDefaultRegistry()
	if r == nil {
		t.Fatal("NewDefaultRegistry returned nil")
	}
	if len(r.All()) == 0 {
		t.Fatal("expected at least one analyzer")
	}
}

func TestDetectLanguages_Go(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	langs := DetectLanguages(dir)
	if !contains(langs, "go") {
		t.Errorf("expected go, got %v", langs)
	}
}

func TestDetectLanguages_PythonScript(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "script.py"), []byte("print('hello')\n"), 0644)
	langs := DetectLanguages(dir)
	if !contains(langs, "python") {
		t.Errorf("expected python, got %v", langs)
	}
}

func TestDetectLanguages_JS(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0644)
	langs := DetectLanguages(dir)
	if !contains(langs, "javascript") {
		t.Errorf("expected javascript, got %v", langs)
	}
}

func TestDetectLanguages_TSWithConfig(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte("{}\n"), 0644)
	langs := DetectLanguages(dir)
	if !contains(langs, "typescript") {
		t.Errorf("expected typescript, got %v", langs)
	}
}

func TestDetectLanguages_Shell(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "script.sh"), []byte("#!/bin/sh\n"), 0644)
	langs := DetectLanguages(dir)
	if !contains(langs, "shell") {
		t.Errorf("expected shell, got %v", langs)
	}
}

func TestDetectLanguages_Rust(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\n"), 0644)
	langs := DetectLanguages(dir)
	if !contains(langs, "rust") {
		t.Errorf("expected rust, got %v", langs)
	}
}

func TestDetectLanguages_Empty(t *testing.T) {
	dir := t.TempDir()
	langs := DetectLanguages(dir)
	if len(langs) != 0 {
		t.Errorf("expected no languages for empty dir, got %v", langs)
	}
}

func TestScan(t *testing.T) {
	r, err := Scan(context.Background(), ".")
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}
	if r == nil {
		t.Fatal("Scan returned nil report")
	}
}

func TestGodPkgAnalyzer(t *testing.T) {
	a := godPkgAnalyzer{}
	ff, err := a.Run(context.Background(), ".")
	if err != nil {
		t.Fatalf("godPkgAnalyzer.Run error: %v", err)
	}
	for _, f := range ff {
		if f.Analyzer != "god-package" {
			t.Errorf("analyzer = %q", f.Analyzer)
		}
		if f.Layer != "architecture" {
			t.Errorf("layer = %q", f.Layer)
		}
	}
}

func TestE2E(t *testing.T) {
	r, err := Scan(context.Background(), ".")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if r.Summary.Total != len(r.Findings) {
		t.Errorf("Summary.Total=%d != len(Findings)=%d", r.Summary.Total, len(r.Findings))
	}
	_ = r.JSON()
	_ = r.Terminal()
}

// ─── mock analyzer ───

type mockAnalyzer struct {
	name  string
	layer string
	langs []string
}

func (m *mockAnalyzer) Name() string        { return m.name }
func (m *mockAnalyzer) Layer() string       { return m.layer }
func (m *mockAnalyzer) Languages() []string { return m.langs }
func (m *mockAnalyzer) Run(_ context.Context, _ string) ([]Finding, error) {
	return []Finding{{Severity: SevMedium, Analyzer: m.name, File: "mock.go", Line: 1, Message: "mock finding"}}, nil
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
