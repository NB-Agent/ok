// Copyright 2026 OK Authors
// SPDX-License-Identifier: LicenseRef-OK

package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/NB-Agent/ok/internal/tool"
)

var criticalTools = []struct {
	name string
	ro   bool
}{
	{"bash", false},
	{"git", false},
	{"edit_file", false},
	{"write_file", false},
	{"read_file", true},
	{"grep", true},
	{"glob", true},
	{"database", false},
	{"deploy", false},
	{"ls", true},
	{"digest", true},
}

func TestCriticalToolsIdentity(t *testing.T) {
	t.Parallel()
	for _, ct := range criticalTools {
		t.Run(ct.name, func(t *testing.T) {
			tl, ok := tool.LookupBuiltin(ct.name)
			if !ok {
				t.Fatalf("tool %q not found in registry", ct.name)
			}
			if tl.Name() != ct.name {
				t.Fatalf("tool.Name() = %q, want %q", tl.Name(), ct.name)
			}
			if tl.Description() == "" {
				t.Errorf("tool %q has empty description", ct.name)
			}
			if !json.Valid(tl.Schema()) {
				t.Errorf("tool %q schema is not valid JSON: %s", ct.name, string(tl.Schema()))
			}
			if tl.ReadOnly() != ct.ro {
				t.Errorf("tool %q ReadOnly() = %v, want %v", ct.name, tl.ReadOnly(), ct.ro)
			}
		})
	}
}

func TestCriticalToolsNoPanic(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"database", map[string]any{"query": ""}},
		{"edit_file", map[string]any{}},
		{"write_file", map[string]any{}},
		{"bash", map[string]any{}},
		{"glob", map[string]any{}},
		{"grep", map[string]any{}},
		{"ls", map[string]any{}},
		{"deploy", map[string]any{}},
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/missing-args", c.tool), func(t *testing.T) {
			tl, ok := tool.LookupBuiltin(c.tool)
			if !ok {
				t.Skipf("tool %q not in registry", c.tool)
			}
			args, _ := json.Marshal(c.args)
			_, err := tl.Execute(context.Background(), args)
			if err == nil {
				t.Logf("tool %q accepted empty args (may be intentional)", c.tool)
			}
		})
	}
}
