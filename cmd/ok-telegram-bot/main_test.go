package main

import (
	"testing"
	"time"
)

func TestNewTelegramAdapter(t *testing.T) {
	a := newTelegramAdapter("123:ABC")
	if a.baseURL != "https://api.telegram.org/bot123:ABC" {
		t.Errorf("baseURL = %q", a.baseURL)
	}
	if a.client.Timeout != 60*time.Second {
		t.Errorf("timeout = %v, want 60s", a.client.Timeout)
	}
}

func TestTelegram_PlatformName(t *testing.T) {
	a := newTelegramAdapter("tok")
	if got := a.PlatformName(); got != "telegram" {
		t.Errorf("PlatformName = %q, want %q", got, "telegram")
	}
}

func TestEscapeMarkdown(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"hello_world", "hello\\_world"},
		{"*bold*", "\\*bold\\*"},
		{"[link](url)", "\\[link\\]\\(url\\)"},
		{"mix_of_all", "mix\\_of\\_all"},
		{"", ""},
		{"text!", "text\\!"},
	}
	for _, tc := range tests {
		got := escapeMarkdown(tc.input)
		if got != tc.want {
			t.Errorf("escapeMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestEscapeMarkdown_AllSpecialChars(t *testing.T) {
	input := "_*[]()~`>#+-=|{}.!"
	got := escapeMarkdown(input)
	for _, ch := range []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"} {
		found := false
		for i := 0; i < len(got)-1; i++ {
			if got[i] == '\\' && got[i+1] == ch[0] {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("char %q not escaped in result %q", ch, got)
		}
	}
}
