// @ok/translate — MCP plugin: AI translation via LLM API. (migrated to plugin.StdioServer)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/NB-Agent/ok/internal/plugin"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

type server struct{}

func (server) Info() (string, string) { return "ok-translate", "1.0.0" }

func (server) Tools() []plugin.ToolDef {
	return []plugin.ToolDef{{
		Name:        "translate",
		Description: "Translate text between languages using AI",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": plugin.StrProp(),
				"target": plugin.StrProp(),
				"text":   plugin.StrProp(),
			},
			"required": []string{"text", "target"},
		},
	}}
}

func (server) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	if name != "translate" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	var p struct{ Source, Target, Text string }
	json.Unmarshal(args, &p)
	if p.Text == "" || p.Target == "" {
		return "", fmt.Errorf("text and target are required")
	}
	if p.Source == "" {
		p.Source = "auto"
	}
	return llmTranslate(p.Source, p.Target, p.Text)
}

func llmTranslate(source, target, text string) (string, error) {
	apiKey := os.Getenv("OK_API_KEY")
	model := os.Getenv("OK_MODEL")
	if apiKey == "" {
		apiKey = os.Getenv("DEEPSEEK_API_KEY")
		model = "deepseek-chat"
	}
	if model == "" {
		model = "deepseek-chat"
	}
	prompt := fmt.Sprintf("Translate the following text from %s to %s. Return ONLY the translated text, no explanations.\n\nText: %s", source, target, text)
	body, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.3,
	})
	if err != nil {
		return "", fmt.Errorf("marshal translate request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.deepseek.com/v1/chat/completions", bytes.NewReader(body))
	if reqErr != nil {
		return "", fmt.Errorf("new translate request: %w", reqErr)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("api: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "translate fetch close: %v\n", err)
		}
	}()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &r) != nil || len(r.Choices) == 0 {
		return "", fmt.Errorf("unexpected api response")
	}
	return r.Choices[0].Message.Content, nil
}

func main() { plugin.RunStdio(server{}) }
