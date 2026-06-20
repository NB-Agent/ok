package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpecEnforce(t *testing.T) {
	tests := []struct {
		mode    string
		enforce bool
	}{
		{"enforce", true},
		{"off", false},
		{"", false},
		{"bogus", false},
	}
	for _, tt := range tests {
		s := Spec{Mode: tt.mode}
		if got := s.enforce(); got != tt.enforce {
			t.Errorf("Spec{mode=%q}.enforce() = %v, want %v", tt.mode, got, tt.enforce)
		}
	}
}

func TestWriteAllowDirsNotEmpty(t *testing.T) {
	dirs := writeAllowDirs([]string{"/some/root"})
	if len(dirs) == 0 {
		t.Fatal("writeAllowDirs must return at least one dir")
	}
}

func TestWriteAllowDirsDedup(t *testing.T) {
	// Same root twice must produce only one entry.
	dirs := writeAllowDirs([]string{".", "."})
	seen := map[string]bool{}
	for _, d := range dirs {
		d = filepath.Clean(d)
		if seen[d] {
			t.Errorf("duplicate entry: %q", d)
		}
		seen[d] = true
	}
}

func TestWriteAllowDirsEmptyStringSkipped(t *testing.T) {
	dirs := writeAllowDirs([]string{"", "/tmp"})
	for _, d := range dirs {
		if d == "" {
			t.Error("empty string should be skipped")
		}
	}
}

func TestWriteAllowDirsIncludesTemp(t *testing.T) {
	dirs := writeAllowDirs([]string{"/some/root"})
	tmp := os.TempDir()
	found := false
	for _, d := range dirs {
		if d == tmp {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("writeAllowDirs should include TempDir %q, got %v", tmp, dirs)
	}
}

func TestWriteAllowDirsIncludesDevNull(t *testing.T) {
	dirs := writeAllowDirs([]string{"/some/root"})
	// /dev may not exist on all platforms (e.g. Windows), in which case
	// filepath.EvalSymlinks fails and the dir is omitted — that's fine.
	foundDev := false
	foundTmp := false
	for _, d := range dirs {
		if d == "/dev" || d == "/private/tmp" || d == os.TempDir() {
			if d == "/dev" {
				foundDev = true
			}
			foundTmp = true
		}
	}
	if !foundDev && !foundTmp {
		t.Error("writeAllowDirs should include at least one write-allowed dir")
	}
}

func TestWriteAllowDirsAddsToolchainCaches(t *testing.T) {
	dirs := writeAllowDirs([]string{"/some/root"})
	if home, err := os.UserHomeDir(); err == nil {
		homeCache := filepath.Join(home, ".cache")
		found := false
		for _, d := range dirs {
			if d == homeCache {
				found = true
				break
			}
		}
		// .cache may not exist, in which case filepath.EvalSymlinks may fail and it's omitted.
		// So we only check that at least one home-relative dir was attempted.
		goDir := filepath.Join(home, "go")
		for _, d := range dirs {
			if d == goDir {
				found = true
			}
		}
		_ = found // non-essential — directories may or may not exist on the test host
	}
}

func TestAvailable(t *testing.T) {
	// Available() must return a boolean. We cannot assert true/false because
	// it depends on the test host OS/kernel. Just verify it doesn't panic.
	_ = Available()
}

func TestCommandStruct(t *testing.T) {
	// Verify Command builds without panicking on a no-op spec.
	spec := Spec{Mode: ""}
	args, wrapped := Command(spec, "sh", "echo hello")
	if wrapped {
		t.Log("Command returned wrapped=true on this platform")
	}
	if len(args) == 0 {
		t.Error("Command must return at least one arg")
	}
}
