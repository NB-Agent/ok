package computeruse

import (
	"testing"
)

func TestNewComputerUse(t *testing.T) {
	cu := NewComputerUse("test-key", "https://api.test.com", "test-model")
	if cu == nil {
		t.Fatal("NewComputerUse returned nil")
	}
	if cu.apiKey != "test-key" {
		t.Errorf("apiKey = %q, want test-key", cu.apiKey)
	}
	if cu.baseURL != "https://api.test.com" {
		t.Errorf("baseURL = %q, want https://api.test.com", cu.baseURL)
	}
	if cu.model != "test-model" {
		t.Errorf("model = %q, want test-model", cu.model)
	}
	if cu.maxSteps != 20 {
		t.Errorf("maxSteps = %d, want 20", cu.maxSteps)
	}
	if cu.http == nil {
		t.Error("http client should not be nil")
	}
}

func TestComputerActionSchemasComplete(t *testing.T) {
	actions := []string{"click", "double_click", "right_click", "type", "key", "scroll", "done", "fail"}
	found := make(map[string]bool)
	for _, s := range actionSchemas {
		if fn, ok := s["function"].(map[string]any); ok {
			name, _ := fn["name"].(string)
			found[name] = true
		}
	}
	for _, a := range actions {
		if !found[a] {
			t.Errorf("action schema %q not found", a)
		}
	}
}

func TestEscapeSendKeys(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"a+b", "a{+}b"},
		{"^C", "{^}C"},
		{"50%", "50{%}"},
		{"~(parens)", "{~}{(}parens{)}"},
		{"curly{brackets}", "curly{{}brackets{}}"},
		{"it's", "it''s"},
	}
	for _, tt := range tests {
		got := escapeSendKeys(tt.input)
		if got != tt.want {
			t.Errorf("escapeSendKeys(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestComputerUseSystemPrompt(t *testing.T) {
	if computerUseSystemPrompt == "" {
		t.Error("computerUseSystemPrompt should not be empty")
	}
	if len(computerUseSystemPrompt) < 100 {
		t.Error("computerUseSystemPrompt seems too short")
	}
}
