// Copyright 2026 OK Authors
// SPDX-License-Identifier: LicenseRef-OK

//go:build ignore

// Gen translates the English Messages catalog into every target language.
// It reads messages_en.go, calls an LLM to translate each field, and writes
// messages_xx.go files. Existing manual overrides are preserved.
//
// Usage:
//
//	go generate ./internal/i18n/
//	# or:
//	make i18n
//
// Requires: DEEPSEEK_API_KEY, OPENAI_API_KEY, or ANTHROPIC_API_KEY
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/log"
)

type Language struct {
	Tag    string // "zh", "hi", "es"
	Native string // "中文", "हिन्दी", "Español"
	Name   string // "Chinese", "Hindi", "Spanish"
}

var targetLanguages = []Language{
	{Tag: "zh", Native: "中文", Name: "Chinese"},
	{Tag: "hi", Native: "हिन्दी", Name: "Hindi"},
	{Tag: "es", Native: "Español", Name: "Spanish"},
	{Tag: "pt", Native: "Português", Name: "Portuguese"},
	{Tag: "ru", Native: "Русский", Name: "Russian"},
	{Tag: "ar", Native: "العربية", Name: "Arabic"},
	{Tag: "fr", Native: "Français", Name: "French"},
	{Tag: "id", Native: "Bahasa Indonesia", Name: "Indonesian"},
	{Tag: "de", Native: "Deutsch", Name: "German"},
	{Tag: "vi", Native: "Tiếng Việt", Name: "Vietnamese"},
	{Tag: "th", Native: "ไทย", Name: "Thai"},
	{Tag: "tr", Native: "Türkçe", Name: "Turkish"},
	{Tag: "pl", Native: "Polski", Name: "Polish"},
	{Tag: "nl", Native: "Nederlands", Name: "Dutch"},
	{Tag: "it", Native: "Italiano", Name: "Italian"},
	{Tag: "ko", Native: "한국어", Name: "Korean"},
	{Tag: "ja", Native: "日本語", Name: "Japanese"},
	{Tag: "ta", Native: "தமிழ்", Name: "Tamil"},
	{Tag: "bn", Native: "বাংলা", Name: "Bengali"},
	{Tag: "ms", Native: "Bahasa Melayu", Name: "Malay"},
	{Tag: "tl", Native: "Filipino", Name: "Filipino"},
	{Tag: "sw", Native: "Kiswahili", Name: "Swahili"},
	{Tag: "ha", Native: "Hausa", Name: "Hausa"},
	{Tag: "zu", Native: "isiZulu", Name: "Zulu"},
	{Tag: "ur", Native: "اردو", Name: "Urdu"},
	{Tag: "fa", Native: "فارسی", Name: "Persian"},
	{Tag: "ro", Native: "Română", Name: "Romanian"},
	{Tag: "uk", Native: "Українська", Name: "Ukrainian"},
	{Tag: "el", Native: "Ελληνικά", Name: "Greek"},
}

var fieldRe = regexp.MustCompile(`^\s+(\w+):\s+"((?:[^"\\]|\\.)*)"`)
var i18nDir string

func getI18nDir() string {
	wd, _ := os.Getwd()
	for _, d := range []string{filepath.Join(wd, "internal", "i18n"), filepath.Join(wd, "i18n"), wd} {
		if _, err := os.Stat(filepath.Join(d, "messages_en.go")); err == nil {
			return d
		}
	}
	return ""
}

func parseFields(path string) (names, values []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: cannot read %s: %v\n", path, err)
		os.Exit(1)
	}
	for _, line := range strings.Split(string(data), "\n") {
		m := fieldRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		names = append(names, m[1])
		values = append(values, unescape(m[2]))
	}
	return
}

func unescape(s string) string {
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\t", "\t")
	s = strings.ReplaceAll(s, "\\\"", "\"")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}

func escape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

func main() {
	i18nDir = getI18nDir()
	if i18nDir == "" {
		fmt.Fprintln(os.Stderr, "FATAL: cannot find internal/i18n/")
		os.Exit(1)
	}

	enNames, enValues := parseFields(filepath.Join(i18nDir, "messages_en.go"))
	fmt.Printf("🌍 OK i18n Gen — %d fields, %d languages\n\n", len(enNames), len(targetLanguages))

	for _, lang := range targetLanguages {
		generate(lang, enNames, enValues)
	}

	fmt.Println("\n✅ Done. Run `gofmt -w internal/i18n/` to format.")
}

func generate(lang Language, enNames, enValues []string) {
	outPath := filepath.Join(i18nDir, fmt.Sprintf("messages_%s.go", lang.Tag))
	existing := map[string]string{}

	if data, err := os.ReadFile(outPath); err == nil {
		for _, m := range fieldRe.FindAllStringSubmatch(string(data), -1) {
			existing[m[1]] = unescape(m[2])
		}
	}

	toTranslate := map[string]string{} // name → english value
	for i, name := range enNames {
		ev := enValues[i]
		if ex, ok := existing[name]; ok && ex != "" && ex != ev {
			continue // preserved
		}
		toTranslate[name] = ev
	}

	if len(toTranslate) == 0 {
		fmt.Printf("   ✅ %s (%s) — up to date\n", lang.Tag, lang.Native)
		return
	}

	// Translate
	batchSize := 20
	for i := 0; i < len(enNames); i += batchSize {
		end := i + batchSize
		if end > len(enNames) {
			end = len(enNames)
		}
		var prompt string
		for j := i; j < end; j++ {
			name := enNames[j]
			if _, needs := toTranslate[name]; needs {
				prompt += fmt.Sprintf("%s: %s\n", name, enValues[j])
			}
		}
		if prompt == "" {
			continue
		}

		result := translate(lang, prompt)
		for k, v := range result {
			existing[k] = v
		}
	}

	// Write file
	var b bytes.Buffer
	b.WriteString(fmt.Sprintf(`// Code generated by go generate ./internal/i18n/; DO NOT EDIT.

package i18n

// %s is the %s catalog (auto-translated).
var %s = Messages{
`, lang.Name, lang.Native, lang.Name))
	for i, name := range enNames {
		if val, ok := existing[name]; ok && val != "" {
			b.WriteString(fmt.Sprintf("\t%s: %q,\n", name, escape(val)))
		} else {
			b.WriteString(fmt.Sprintf("\t%s: %q,\n", name, escape(enValues[i])))
		}
	}
	b.WriteString("}\n\n")
	b.WriteString(fmt.Sprintf(`func init() {
	registerResolver(%q, &%s)
}
`, lang.Tag, lang.Name))

	os.WriteFile(outPath, b.Bytes(), 0644)
	fmt.Printf("   ✅ %s (%s) — %d fields\n", lang.Tag, lang.Native, len(enNames))
}

func translate(lang Language, fields string) map[string]string {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	endpoint := os.Getenv("OK_TRANSLATOR_ENDPOINT")
	model := os.Getenv("OK_TRANSLATOR_MODEL")

	if apiKey == "" {
		if k := os.Getenv("OPENAI_API_KEY"); k != "" {
			apiKey = k
			endpoint = "https://api.openai.com/v1/chat/completions"
			model = "gpt-4o-mini"
		} else if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
			return translateAnthropic(lang, fields)
		}
	}
	if endpoint == "" {
		endpoint = "https://api.deepseek.com/chat/completions"
	}
	if model == "" {
		model = "deepseek-chat"
	}

	prompt := fmt.Sprintf(`Translate these UI strings to %s (%s). Rules:
- Keep %%s, %%d, %%q, %%v format verbs exactly as-is
- Keep HTML tags as-is
- Keep \n as-is
- Return ONLY a raw JSON object, no markdown

%s`, lang.Native, lang.Tag, fields)

	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a translator. Return valid JSON only."},
			{"role": "user", "content": prompt},
		},
		"temperature": 0.1,
	}
	data, err := json.Marshal(body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: %s marshal for %s: %v\n", endpoint, lang.Tag, err)
		return map[string]string{}
	}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(data))
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: build request for %s: %v\n", lang.Tag, err)
		return map[string]string{}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: %s call for %s: %v\n", endpoint, lang.Tag, err)
		return map[string]string{}
	}
	defer log.Close("i18n gen response", resp.Body)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: read body for %s: %v\n", lang.Tag, err)
		return map[string]string{}
	}
	var llmResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &llmResp); err != nil || len(llmResp.Choices) == 0 {
		return map[string]string{}
	}

	content := llmResp.Choices[0].Message.Content
	var result map[string]string
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		if idx := strings.Index(content, "{"); idx >= 0 {
			if end := strings.LastIndex(content, "}"); end > idx {
				json.Unmarshal([]byte(content[idx:end+1]), &result)
			}
		}
	}
	if result == nil {
		result = map[string]string{}
	}
	return result
}

func translateAnthropic(lang Language, fields string) map[string]string {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	model := os.Getenv("OK_TRANSLATOR_MODEL")
	if model == "" {
		model = "claude-3-5-haiku-latest"
	}

	prompt := fmt.Sprintf(`Translate these UI strings to %s (%s). Rules:
- Keep %%s, %%d, %%q, %%v format verbs exactly as-is
- Keep HTML tags as-is
- Keep \n as-is
- Return ONLY a raw JSON object, no markdown

%s`, lang.Native, lang.Tag, fields)

	body := map[string]any{
		"model":       model,
		"max_tokens":  4096,
		"temperature": 0.1,
		"system":      "You are a translator. Return valid JSON only.",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: anthropic marshal for %s: %v\n", lang.Tag, err)
		return map[string]string{}
	}
	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(data))
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: anthropic build request for %s: %v\n", lang.Tag, err)
		return map[string]string{}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: anthropic call for %s: %v\n", lang.Tag, err)
		return map[string]string{}
	}
	defer log.Close("i18n gen response", resp.Body)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: anthropic read body for %s: %v\n", lang.Tag, err)
		return map[string]string{}
	}
	var anthroResp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &anthroResp); err != nil || len(anthroResp.Content) == 0 {
		return map[string]string{}
	}

	content := anthroResp.Content[0].Text
	var result map[string]string
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		if idx := strings.Index(content, "{"); idx >= 0 {
			if end := strings.LastIndex(content, "}"); end > idx {
				json.Unmarshal([]byte(content[idx:end+1]), &result)
			}
		}
	}
	if result == nil {
		result = map[string]string{}
	}
	return result
}
