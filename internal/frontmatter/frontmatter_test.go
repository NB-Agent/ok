package frontmatter

import (
	"testing"
)

func TestParseNoFrontmatter(t *testing.T) {
	fm, body := Parse("just a body\ntwo lines")
	if len(fm) != 0 {
		t.Errorf("expected empty frontmatter, got %v", fm)
	}
	if body != "just a body\ntwo lines" {
		t.Errorf("body = %q", body)
	}
}

func TestParseEmptyInput(t *testing.T) {
	fm, body := Parse("")
	if len(fm) != 0 {
		t.Error("expected empty frontmatter")
	}
	if body != "" {
		t.Error("expected empty body")
	}
}

func TestParseFullFrontmatter(t *testing.T) {
	input := `---
name: test-skill
description: A test
---
This is the body
over two lines.`
	fm, body := Parse(input)
	if fm["name"] != "test-skill" {
		t.Errorf("name = %q", fm["name"])
	}
	if fm["description"] != "A test" {
		t.Errorf("description = %q", fm["description"])
	}
	if body != "This is the body\nover two lines." {
		t.Errorf("body = %q", body)
	}
}

func TestParseKeyWithQuotes(t *testing.T) {
	input := `---
name: "quoted value"
desc: 'single quoted'
---
body`
	fm, _ := Parse(input)
	if fm["name"] != "quoted value" {
		t.Errorf("name = %q", fm["name"])
	}
	if fm["desc"] != "single quoted" {
		t.Errorf("desc = %q", fm["desc"])
	}
}

func TestParseUnclosedFrontmatter(t *testing.T) {
	// Unclosed --- fence: treat all as body (conservative).
	input := `---
name: orphan
body here`
	fm, body := Parse(input)
	if len(fm) != 0 {
		t.Errorf("unclosed frontmatter should return empty map, got %v", fm)
	}
	if body != input {
		t.Errorf("unclosed body = %q", body)
	}
}

func TestParseIndentedKeyFlattening(t *testing.T) {
	// "  type: reference" should flatten to fm["type"]
	input := `---
name: my-skill
metadata:
  type: reference
---
body`
	fm, _ := Parse(input)
	if fm["name"] != "my-skill" {
		t.Errorf("name = %q", fm["name"])
	}
	if fm["type"] != "reference" {
		t.Errorf("type = %q", fm["type"])
	}
	if _, exists := fm["metadata"]; exists {
		t.Error("metadata key should not appear — value is empty, deferred to indented lines")
	}
}

func TestParseSectionHeaderSkipped(t *testing.T) {
	// "metadata:" with no value — skipped (value is on indented lines).
	input := `---
name: test
metadata:
---
body`
	fm, _ := Parse(input)
	if fm["name"] != "test" {
		t.Errorf("name = %q", fm["name"])
	}
	if _, exists := fm["metadata"]; exists {
		t.Error("metadata (valueless section header) should not appear")
	}
}

func TestParseLastWriteWins(t *testing.T) {
	input := `---
key: first
key: second
---
body`
	fm, _ := Parse(input)
	if fm["key"] != "second" {
		t.Errorf("last write wins: key = %q, want 'second'", fm["key"])
	}
}

func TestParseOnlyFenceLine(t *testing.T) {
	// Single "---" line, no closing fence — unclosed → all body.
	input := "---\njust text"
	fm, body := Parse(input)
	if len(fm) != 0 {
		t.Error("expected empty frontmatter")
	}
	if body != input {
		t.Errorf("body = %q", body)
	}
}

func TestParseBodyWithFencesInContent(t *testing.T) {
	input := `---
name: md-demo
---
The body has --- inside it
and also --- more dashes.`
	fm, body := Parse(input)
	if fm["name"] != "md-demo" {
		t.Errorf("name = %q", fm["name"])
	}
	if body != "The body has --- inside it\nand also --- more dashes." {
		t.Errorf("body = %q", body)
	}
}

func TestNormalizeRemovesBOM(t *testing.T) {
	raw := []byte("\xEF\xBB\xBF---\nkey: value\n---\nbody")
	result := Normalize(raw)
	if result[0] != '-' {
		t.Fatalf("BOM should be stripped, got U+%04X", result[0])
	}
	fm, body := Parse(result)
	if fm["key"] != "value" {
		t.Errorf("key = %q", fm["key"])
	}
	if body != "body" {
		t.Errorf("body = %q", body)
	}
}

func TestNormalizeCRLF(t *testing.T) {
	raw := []byte("---\r\nkey: value\r\n---\r\nbody")
	result := Normalize(raw)
	if result != "---\nkey: value\n---\nbody" {
		t.Errorf("CRLF should become LF: %q", result)
	}
	fm, body := Parse(result)
	if fm["key"] != "value" {
		t.Errorf("key = %q", fm["key"])
	}
	if body != "body" {
		t.Errorf("body = %q", body)
	}
}

func TestParseEmptyFrontmatter(t *testing.T) {
	input := `---
---
body only`
	fm, body := Parse(input)
	if len(fm) != 0 {
		t.Errorf("empty frontmatter block, got %v", fm)
	}
	if body != "body only" {
		t.Errorf("body = %q", body)
	}
}

func TestNormalizePlainText(t *testing.T) {
	// No BOM, no CRLF — should pass through.
	raw := []byte("hello world")
	result := Normalize(raw)
	if result != "hello world" {
		t.Errorf("plain text passes through: %q", result)
	}
}

func TestParseReturnedMapIsNotNil(t *testing.T) {
	fm, _ := Parse("no frontmatter")
	if fm == nil {
		t.Error("Parse should return non-nil map even when no frontmatter")
	}
}

func TestParseRoundTrip(t *testing.T) {
	// Parse should not mutate the body other than stripping the frontmatter.
	wantBody := "line1\nline2\nline3"
	input := "---\nname: test\n---\n" + wantBody
	_, body := Parse(input)
	if body != wantBody {
		t.Errorf("body = %q, want %q", body, wantBody)
	}
}

func TestParseKeysAreLowercased(t *testing.T) {
	input := `---
NAME: test
Description: desc
---
body`
	fm, _ := Parse(input)
	if _, ok := fm["name"]; !ok {
		t.Error("NAME should lowercase to name")
	}
	if _, ok := fm["description"]; !ok {
		t.Error("Description should lowercase to description")
	}
	if _, ok := fm["NAME"]; ok {
		t.Error("uppercase NAME should not exist")
	}
}

func TestParseTrailingAndBlankLines(t *testing.T) {
	input := "---\nkey: value\n---\n\n\n"
	fm, body := Parse(input)
	if fm["key"] != "value" {
		t.Errorf("key = %q", fm["key"])
	}
	// Trailing newlines are preserved — they're part of the body.
	if body != "\n\n" {
		t.Errorf("body = %q, want trailing newlines preserved", body)
	}
}
