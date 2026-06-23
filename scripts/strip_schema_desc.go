// strip_schema_desc removes property-level "description" keys from all
// json.RawMessage tool schemas under internal/tool/builtin/.  The tool-level
// Description() methods are left untouched — only JSON Schema property
// descriptions (redundant with param names, enums, and tool description) are
// stripped.
//
// Run: go run scripts/strip_schema_desc.go

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	dir := filepath.Join("internal", "tool", "builtin")
	failed := 0
	changed := 0

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		orig, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
			failed++
			return nil
		}
		newb := processFile(orig)
		if bytes.Equal(orig, newb) {
			return nil
		}
		if err := os.WriteFile(path, newb, info.Mode()); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			failed++
			return nil
		}
		saved := len(orig) - len(newb)
		fmt.Printf("%-30s %5d → %5d  (-%d bytes)\n", filepath.Base(path), len(orig), len(newb), saved)
		changed++
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "walk: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n%d files changed, %d failed\n", changed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

var re = regexp.MustCompile(`json\.RawMessage\(\x60([^\x60]*)\x60\)`)

func processFile(src []byte) []byte {
	return re.ReplaceAllFunc(src, func(match []byte) []byte {
		// match: json.RawMessage(`...`)
		end := len(match)
		start := bytes.IndexByte(match, '`') + 1
		jsonStr := string(match[start : end-2]) // -2 for "`)"
		if end-1 >= len(match) || match[end-1] != ')' {
			return match
		}

		var v interface{}
		if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
			return match // not valid JSON, leave untouched
		}
		stripDescriptions(v)

		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return match
		}
		out := bytes.TrimSpace(buf.Bytes())
		return []byte(fmt.Sprintf("json.RawMessage(`%s`)", string(out)))
	})
}

func stripDescriptions(v interface{}) {
	switch vv := v.(type) {
	case map[string]interface{}:
		delete(vv, "description")
		for _, val := range vv {
			stripDescriptions(val)
		}
	case []interface{}:
		for _, val := range vv {
			stripDescriptions(val)
		}
	}
}
