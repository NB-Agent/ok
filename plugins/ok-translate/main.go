// @ok/translate — MCP plugin: AI translation via LLM API.
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
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

func main() {
	s := &mcpServer{name: "ok-translate", version: "1.0.0"}
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
				"name": "translate", "description": "Translate text between languages using AI",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
					"source": map[string]any{"type": "string"},
					"target": map[string]any{"type": "string"},
					"text":   map[string]any{"type": "string"},
				}, "required": []string{"text", "target"}},
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

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
