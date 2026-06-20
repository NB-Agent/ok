package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkLoadFromSmall(b *testing.B) {
	dir := b.TempDir()
	os.WriteFile(filepath.Join(dir, "REASONIX.md"), []byte("# Project\n\n- Use tabs\n- Go version 1.25\n"), 0644)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sources := loadFrom(dir, []string{"REASONIX.md"}, ScopeProject)
		if len(sources) != 1 {
			b.Fatalf("expected 1 source, got %d", len(sources))
		}
	}
}

func BenchmarkLoadFromWithImports(b *testing.B) {
	dir := b.TempDir()
	os.WriteFile(filepath.Join(dir, "REASONIX.md"), []byte("# Root\n\n@imports/core.md\n"), 0644)
	core := strings.Repeat("# Core\n\nThis is the core module documentation.\n\n", 20)
	os.WriteFile(filepath.Join(dir, "imports", "core.md"), []byte(core), 0644)
	os.Mkdir(filepath.Join(dir, "imports"), 0755)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sources := loadFrom(dir, []string{"REASONIX.md"}, ScopeProject)
		if len(sources) != 1 {
			b.Fatalf("expected 1 source, got %d", len(sources))
		}
	}
}
