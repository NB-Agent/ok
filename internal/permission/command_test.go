package permission

import (
	"encoding/json"
	"testing"
)

func TestCleanCommand(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"npm install", "npm install"},
		{"timeout 30s npm install", "npm install"},
		{"nice -n 10 npm install", "npm install"},
		{"npx eslint .", "npx eslint ."},
		{"sudo apt-get update", "apt-get update"},
		{"", ""},
		{"timeout 30s nice -n 10 npm install", "npm install"},
		{"nohup sleep 100 &", "sleep 100 &"},
		{"env GOOS=linux go build", "go build"},
		{"/usr/bin/python3 test.py", "python3 test.py"},
		{"docker compose up", "docker compose up"},
		{"go build ./...", "go build ./..."},
		{"bash -c \"echo hello\"", "echo hello"},
		{"sh -c 'ls -la'", "ls -la"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := CleanCommand(tt.input)
			if got != tt.expected {
				t.Errorf("CleanCommand(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"npm install", []string{"npm", "install"}},
		{"timeout 30s nice -n 10 npm install", []string{"timeout", "30s", "nice", "-n", "10", "npm", "install"}},
		{"echo 'hello world'", []string{"echo", "hello world"}},
		{`echo "hello world"`, []string{"echo", "hello world"}},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := tokenize(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("tokenize(%q) = %v (len=%d), want %v (len=%d)",
					tt.input, got, len(got), tt.expected, len(tt.expected))
				return
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("tokenize(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestStripShellWrappers(t *testing.T) {
	tests := []struct {
		input      string
		wantBinary string
		wantRest   string
	}{
		{"npm install", "npm", "install"},
		{"timeout 30s npm install", "npm", "install"},
		{"nice -n 10 npm install", "npm", "install"},
		{"sudo apt-get update", "apt-get", "update"},
		{"npx eslint .", "npx", "eslint ."},
		{"", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			gotBin, gotRest := stripShellWrappers(tt.input)
			if gotBin != tt.wantBinary || gotRest != tt.wantRest {
				t.Errorf("stripShellWrappers(%q) = (%q, %q), want (%q, %q)",
					tt.input, gotBin, gotRest, tt.wantBinary, tt.wantRest)
			}
		})
	}
}

func TestSubjectIntegration(t *testing.T) {
	tests := []struct {
		name     string
		args     json.RawMessage
		expected string
	}{
		{
			name:     "command with wrapper",
			args:     json.RawMessage(`{"command": "timeout 30s nice -n 10 npm install"}`),
			expected: "npm install",
		},
		{
			name:     "plain command",
			args:     json.RawMessage(`{"command": "npm install"}`),
			expected: "npm install",
		},
		{
			name:     "file_path unaffected",
			args:     json.RawMessage(`{"file_path": "/tmp/test.txt"}`),
			expected: "/tmp/test.txt",
		},
		{
			name:     "empty args",
			args:     json.RawMessage(`{}`),
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Subject(tt.args)
			if got != tt.expected {
				t.Errorf("Subject(%s) = %q, want %q", string(tt.args), got, tt.expected)
			}
		})
	}
}
