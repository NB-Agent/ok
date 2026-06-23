// @ok/ocr — MCP plugin: OCR via Tesseract or LLM fallback. (migrated to plugin.StdioServer)
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/plugin"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

type server struct{}

func (server) Info() (string, string) { return "ok-ocr", "1.0.0" }

func (server) Tools() []plugin.ToolDef {
	return []plugin.ToolDef{{
		Name:        "ocr",
		Description: "Extract text from images using OCR or AI vision",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":     plugin.StrProp(),
				"language": plugin.StrProp(),
			},
			"required": []string{"path"},
		},
	}}
}

func (server) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	if name != "ocr" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	var p struct{ Path, Language string }
	json.Unmarshal(args, &p)
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	return ocrImage(p.Path, p.Language)
}

func ocrImage(path, language string) (string, error) {
	if err := safePluginPath(path); err != nil {
		return "", err
	}
	if _, err := exec.LookPath("tesseract"); err == nil {
		args := []string{"--", path, "stdout"}
		if language != "" {
			args = append([]string{"-l", language}, args...)
		}
		out, err := exec.Command("tesseract", args...).Output()
		if err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}
	return llmOCR(path), nil
}

func llmOCR(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	apiKey := os.Getenv("OK_API_KEY")
	if apiKey == "" {
		return "OCR requires either Tesseract CLI or OK_API_KEY env var"
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	body, err := json.Marshal(map[string]any{
		"model": "deepseek-chat",
		"messages": []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "text", "text": "Extract all text from this image. Return only the extracted text."},
				{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + b64}},
			}},
		},
	})
	if err != nil {
		return fmt.Sprintf("marshal ocr request: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.deepseek.com/v1/chat/completions",
		strings.NewReader(string(body)))
	if reqErr != nil {
		return fmt.Sprintf("new ocr request: %v", reqErr)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Sprintf("api error: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "ocr fetch close: %v\n", err)
		}
	}()
	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("read response: %v", err)
	}
	return string(respData)
}

func main() { plugin.RunStdio(server{}) }

// safePluginPath rejects absolute paths, .. traversal, and leading dashes.
func safePluginPath(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if p[0] == '-' {
		return fmt.Errorf("invalid path (starts with '-'): %s", p)
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("absolute paths are not allowed: %s", p)
	}
	clean := filepath.Clean(p)
	if strings.HasPrefix(clean, "..") && (len(clean) == 2 || clean[2] == '/' || clean[2] == '\\') {
		return fmt.Errorf("path traversal not allowed: %s", p)
	}
	if strings.Contains(clean, "/..") || strings.Contains(clean, "\\..") {
		return fmt.Errorf("path traversal not allowed: %s", p)
	}
	return nil
}
