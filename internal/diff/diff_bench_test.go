package diff

import (
	"strings"
	"testing"
)

func BenchmarkBuildSmallFile(b *testing.B) {
	old := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	new := "package main\n\nfunc main() {\n\tfmt.Println(\"world\")\n}\n"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Build("test.go", old, new, Modify)
	}
}

func BenchmarkBuildLargeFile(b *testing.B) {
	old := strings.Repeat("line of code here\n", 2000)
	new := strings.Repeat("line of code here\n", 1990) +
		"inserted line\n" +
		strings.Repeat("line of code here\n", 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Build("large.go", old, new, Modify)
	}
}

func BenchmarkBuildIdentical(b *testing.B) {
	s := strings.Repeat("package main\n\nfunc main() {\n\tfmt.Println(\"x\")\n}\n", 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Build("same.go", s, s, Modify)
	}
}

func BenchmarkBuildCreate(b *testing.B) {
	body := strings.Repeat("new file content line\n", 500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Build("new.go", "", body, Create)
	}
}
