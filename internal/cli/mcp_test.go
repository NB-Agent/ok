package cli

import (
	"reflect"
	"testing"
)

func TestParseMCPAddStdio(t *testing.T) {
	e, err := parseMCPAdd([]string{"fs", "npx", "-y", "@modelcontextprotocol/server-filesystem", "."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Name != "fs" || e.Command != "npx" {
		t.Fatalf("name/command = %q/%q", e.Name, e.Command)
	}
	// The command keeps its own -flags: "-y" is an arg, not parsed as our flag.
	if want := []string{"-y", "@modelcontextprotocol/server-filesystem", "."}; !reflect.DeepEqual(e.Args, want) {
		t.Fatalf("args = %v, want %v", e.Args, want)
	}
	if e.URL != "" {
		t.Errorf("stdio entry should have no URL, got %q", e.URL)
	}
}

func TestParseMCPAddStdioEnv(t *testing.T) {
	e, err := parseMCPAdd([]string{"db", "--env", "PGHOST=localhost", "node", "server.js"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Command != "node" || !reflect.DeepEqual(e.Args, []string{"server.js"}) {
		t.Fatalf("command/args = %q/%v", e.Command, e.Args)
	}
	if e.Env["PGHOST"] != "localhost" {
		t.Errorf("env PGHOST = %q, want localhost", e.Env["PGHOST"])
	}
}

func TestParseMCPAddHTTP(t *testing.T) {
	for _, args := range [][]string{
		{"stripe", "--http", "https://mcp.stripe.com"},
		{"stripe", "--http=https://mcp.stripe.com"},
	} {
		e, err := parseMCPAdd(args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if e.Type != "http" || e.URL != "https://mcp.stripe.com" {
			t.Errorf("%v -> type/url = %q/%q", args, e.Type, e.URL)
		}
		if e.Command != "" {
			t.Errorf("%v -> remote entry should have no command, got %q", args, e.Command)
		}
	}
}

func TestParseMCPAddHTTPHeader(t *testing.T) {
	e, err := parseMCPAdd([]string{"x", "--http", "https://x", "--header", "Authorization=Bearer abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Headers["Authorization"] != "Bearer abc" {
		t.Errorf("header = %q, want %q", e.Headers["Authorization"], "Bearer abc")
	}
}

func TestParseMCPAddErrors(t *testing.T) {
	cases := map[string][]string{
		"no name":           {},
		"name is a flag":    {"--http", "https://x"},
		"no command/url":    {"fs"},
		"command and url":   {"x", "--http", "https://x", "node"},
		"unknown flag":      {"x", "--bogus", "y", "cmd"},
		"env without value": {"x", "--env"},
	}
	for name, args := range cases {
		if _, err := parseMCPAdd(args); err == nil {
			t.Errorf("%s: expected an error for %v", name, args)
		}
	}
}

func TestTokenizeArgs(t *testing.T) {
	got := tokenizeArgs(`/mcp add s --header "Authorization=Bearer abc" --http https://x`)
	want := []string{"/mcp", "add", "s", "--header", "Authorization=Bearer abc", "--http", "https://x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenizeArgs = %v, want %v", got, want)
	}
	// Single quotes work too, and surrounding whitespace collapses.
	if got := tokenizeArgs("  a  'b c'  d "); !reflect.DeepEqual(got, []string{"a", "b c", "d"}) {
		t.Fatalf("tokenizeArgs single-quote = %v", got)
	}
}
