package builtin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/log"
	"github.com/NB-Agent/ok/internal/tool"
)

// ocrTool extracts text from images using the LLM's vision capabilities.
// It is NOT registered via init() — boot.go wires it at assembly time like translate.
type ocrTool struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

// NewOCRTool creates a Tool that extracts text from images via LLM vision API.
func NewOCRTool(baseURL, apiKey, model string) tool.Tool {
	return &ocrTool{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (o *ocrTool) Name() string { return "ocr" }

func (o *ocrTool) Description() string {
	return "Extract text from images using AI vision. Supports screenshots, photos, scanned documents, and any image file. Returns the text content found in the image."
}

func (o *ocrTool) Schema() json.RawMessage {
	return json.RawMessage(`{"properties":{"language":{"type":"string"},"path":{"type":"string"}},"required":["path"],"type":"object"}`)
}

func (o *ocrTool) ReadOnly() bool { return true }

func (o *ocrTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path     string `json:"path"`
		Language string `json:"language"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	// Read image and encode as base64.
	imgData, err := os.ReadFile(p.Path)
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}
	b64img := base64.StdEncoding.EncodeToString(imgData)

	langHint := ""
	if p.Language != "" {
		langHint = fmt.Sprintf(" The text is in %s.", p.Language)
	}

	prompt := fmt.Sprintf(`Extract ALL text visible in this image. Return only the text content exactly as it appears. Preserve line breaks and layout where meaningful.%s`, langHint)

	body := map[string]any{
		"model": o.model,
		"messages": []map[string]any{
			{"role": "system", "content": "You are an OCR engine. Extract all visible text from images accurately. Return ONLY the extracted text, no commentary."},
			{"role": "user", "content": []map[string]any{
				{"type": "text", "text": prompt},
				{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + b64img}},
			}},
		},
		"temperature": 0,
		"max_tokens":  4096,
	}

	rawBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(rawBody))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer log.Close("ocr response", resp.Body)

	rawResp, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
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

	text := strings.TrimSpace(result.Choices[0].Message.Content)
	return fmt.Sprintf("# OCR\n\n```\n%s\n```\n", text), nil
}
