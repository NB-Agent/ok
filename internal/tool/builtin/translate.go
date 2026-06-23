package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/tool"
)

// translateTool calls the LLM provider's API to translate text between languages.
// It is NOT registered via init() — boot.go wires it at assembly time.
type translateTool struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

// NewTranslateTool creates a Tool that translates text using the configured LLM.
func NewTranslateTool(baseURL, apiKey, model string) tool.Tool {
	return &translateTool{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *translateTool) Name() string { return "translate" }

func (t *translateTool) Description() string {
	return "Translate text between languages using AI. Input: source text, source language, target language. Output: translated text."
}

func (t *translateTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"source":{"type":"string"},"target":{"type":"string"},"text":{"type":"string"}},"required":["text","target"],"type":"object"}`)
}

func (t *translateTool) ReadOnly() bool { return true }

func (t *translateTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Text   string `json:"text"`
		Source string `json:"source"`
		Target string `json:"target"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if p.Target == "" {
		return "", fmt.Errorf("target language is required")
	}
	if p.Source == "" {
		p.Source = "auto"
	}

	prompt := fmt.Sprintf(`Translate the following text from %s to %s. Return ONLY the translated text, no explanations, no quotes.

Text to translate:
---
%s
---`, p.Source, p.Target, p.Text)

	body := map[string]any{
		"model": t.model,
		"messages": []map[string]any{
			{"role": "system", "content": "You are a professional translator. Translate accurately and naturally. Return ONLY the translation."},
			{"role": "user", "content": prompt},
		},
		"temperature": 0,
		"max_tokens":  4096,
	}

	rawBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/chat/completions", bytes.NewReader(rawBody))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer log.Close("translate response", resp.Body)

	rawResp, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API status %d: %s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rawResp, &result); err != nil {
		return "", fmt.Errorf("unmarshal: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	translation := strings.TrimSpace(result.Choices[0].Message.Content)
	return fmt.Sprintf("# Translation (%s → %s)\n\n%s\n", p.Source, p.Target, translation), nil
}
