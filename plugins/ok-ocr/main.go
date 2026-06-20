// @ok/ocr — MCP plugin: OCR via Tesseract or LLM fallback.
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
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

func main() {
	s := &mcpServer{name: "ok-ocr", version: "1.0.0"}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for dec.More() {
		var req jsonRPC
		if err := dec.Decode(&req); err != nil {
			break
		}
		resp := s.handle(req)
		if resp.ID != nil {
			enc.Encode(resp)
		}
	}
}

type jsonRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int
	Message string
}
type mcpServer struct{ name, version string }

func (s *mcpServer) handle(req jsonRPC) jsonRPC {
	id := req.ID
	switch req.Method {
	case "initialize":
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": s.name, "version": s.version},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		})}
	case "tools/list":
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"tools": []map[string]any{{
				"name": "ocr", "description": "Extract text from images using OCR or AI vision",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
					"path":     map[string]any{"type": "string"},
					"language": map[string]any{"type": "string"},
				}, "required": []string{"path"}},
			}},
		})}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &params)
		result, err := s.exec(params.Name, params.Arguments)
		if err != nil {
			return jsonRPC{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32000, Message: err.Error()}}
		}
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"content": []map[string]any{{"type": "text", "text": result}},
		})}
	default:
		return jsonRPC{JSONRPC: "2.0", ID: id}
	}
}

func (s *mcpServer) exec(name string, args json.RawMessage) (string, error) {
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
	if _, err := exec.LookPath("tesseract"); err == nil {
		args := []string{path, "stdout"}
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

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
