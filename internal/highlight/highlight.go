// Package highlight provides syntax highlighting for code blocks using chroma.
// It is designed for terminal output (256-color or truecolor) and degrades
// gracefully when the language is unknown (plain text pass-through).
package highlight

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// DefaultStyle is the chroma style used when no style is specified.
const DefaultStyle = "monokai"

// Code returns syntax-highlighted terminal output for the given source code.
// lang is a hint like "go", "python", "rust", "bash" — when empty or unknown
// the source is returned unmodified. style names a chroma style ("monokai",
// "github", "dracula", etc.); "" selects DefaultStyle.
func Code(lang, source, style string) string {
	if lang == "" {
		lang = detectLang(source)
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(source)
	}
	if lexer == nil {
		return source // unknown language — plain text
	}
	lexer = chroma.Coalesce(lexer)

	if style == "" {
		style = DefaultStyle
	}
	st := styles.Get(style)
	if st == nil {
		st = styles.Fallback
	}

	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return source
	}

	var buf strings.Builder
	it, err := lexer.Tokenise(nil, source)
	if err != nil {
		return source
	}
	if err := formatter.Format(&buf, st, it); err != nil {
		return source
	}
	return buf.String()
}

// detectLang does a basic content-based language guess for common cases.
func detectLang(source string) string {
	s := strings.TrimSpace(source)
	switch {
	case strings.HasPrefix(s, "package ") || strings.HasPrefix(s, "import "),
		strings.HasPrefix(s, "func "), strings.HasPrefix(s, "type "):
		return "go"
	case strings.HasPrefix(s, "def ") || strings.HasPrefix(s, "import "),
		strings.HasPrefix(s, "from "), strings.HasPrefix(s, "class "):
		return "python"
	case strings.HasPrefix(s, "#!/bin/bash"), strings.HasPrefix(s, "#!/bin/sh"),
		strings.HasPrefix(s, "#!"):
		return "bash"
	case strings.HasPrefix(s, "fn ") || strings.HasPrefix(s, "pub "),
		strings.HasPrefix(s, "use "), strings.HasPrefix(s, "mod "):
		return "rust"
	case strings.HasPrefix(s, "const ") || strings.HasPrefix(s, "let "),
		strings.HasPrefix(s, "function "), strings.HasPrefix(s, "//"):
		return "javascript"
	default:
		return ""
	}
}
