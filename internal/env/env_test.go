package env

import (
	"strings"
	"testing"
)

func TestContext_NonEmpty(t *testing.T) {
	s := Context()
	if s == "" {
		t.Fatal("Context() returned empty string")
	}
	if !strings.Contains(s, "# Environment") {
		t.Errorf("Context() missing '# Environment' header: %s", s)
	}
	if !strings.Contains(s, "OS:") {
		t.Errorf("Context() missing 'OS:' key: %s", s)
	}
	if !strings.Contains(s, "Sandbox:") {
		t.Errorf("Context() missing 'Sandbox:' key: %s", s)
	}
}

func TestSandboxStatus_NotEmpty(t *testing.T) {
	s := sandboxStatus()
	if s == "" {
		t.Fatal("sandboxStatus() returned empty string")
	}
}

func TestTerminalMode_NotEmpty(t *testing.T) {
	m := terminalMode()
	if m == "" {
		t.Fatal("terminalMode() returned empty string")
	}
}
