// @ok/browser — MCP plugin: Headless Chrome browser control via DevTools Protocol.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

func main() {
	s := &mcpServer{name: "ok-browser", version: "1.0.0"}
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
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type mcpServer struct{ name, version string }

var chromeURL string
var httpClient = &http.Client{Timeout: 10 * time.Second}

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
		return jsonRPC{JSONRPC: "2.0", ID: id, Result: mustJSON(schemas())}
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

func schemas() map[string]any {
	return map[string]any{"tools": []map[string]any{{
		"name": "browser", "description": "Control a headless Chrome browser",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"action": strEnum("navigate", "click", "type", "screenshot", "text", "eval", "wait", "scroll", "back", "forward", "refresh", "close"),
			"url":    strType(), "selector": strType(), "value": strType(),
			"wait_ms": map[string]any{"type": "integer"},
		}, "required": []string{"action"}},
	}}}
}

func (s *mcpServer) exec(name string, args json.RawMessage) (string, error) {
	if name != "browser" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	var p struct {
		Action   string `json:"action"`
		URL      string `json:"url"`
		Selector string `json:"selector"`
		Value    string `json:"value"`
		WaitMs   int    `json:"wait_ms"`
	}
	json.Unmarshal(args, &p)

	if chromeURL == "" && p.Action != "close" {
		if err := launchChrome(); err != nil {
			return "", fmt.Errorf("launch chrome: %w", err)
		}
	}

	switch p.Action {
	case "navigate":
		return chromeCall("Page.navigate", map[string]any{"url": p.URL})
	case "click":
		expr := fmt.Sprintf("document.querySelector(%s).click()", escJS(p.Selector))
		return chromeCall("Runtime.evaluate", map[string]any{"expression": expr})
	case "type":
		expr := fmt.Sprintf("document.querySelector(%s).value = %s", escJS(p.Selector), escJS(p.Value))
		return chromeCall("Runtime.evaluate", map[string]any{"expression": expr})
	case "screenshot":
		r, err := chromeCallRaw("Page.captureScreenshot", map[string]any{"format": "png"})
		if err != nil {
			return "", err
		}
		var res struct {
			Data string `json:"data"`
		}
		if json.Unmarshal(r, &res) != nil {
			return "", fmt.Errorf("unexpected response")
		}
		return fmt.Sprintf("data:image/png;base64,%s", res.Data), nil
	case "text":
		expr := fmt.Sprintf("document.querySelector(%s)?.innerText || ''", escJS(p.Selector))
		return chromeCall("Runtime.evaluate", map[string]any{"expression": expr})
	case "eval":
		return chromeCall("Runtime.evaluate", map[string]any{"expression": p.Value})
	case "wait":
		expr := fmt.Sprintf("new Promise(r => setTimeout(r, %d))", p.WaitMs)
		return chromeCall("Runtime.evaluate", map[string]any{"expression": expr, "awaitPromise": true})
	case "scroll":
		expr := fmt.Sprintf("window.scrollBy(0, %s)", p.Value)
		return chromeCall("Runtime.evaluate", map[string]any{"expression": expr})
	case "back":
		return chromeCall("Runtime.evaluate", map[string]any{"expression": "window.history.back()"})
	case "forward":
		return chromeCall("Runtime.evaluate", map[string]any{"expression": "window.history.forward()"})
	case "refresh":
		return chromeCall("Page.reload", nil)
	case "close":
		chromeURL = ""
		return "browser closed", nil
	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}

func launchChrome() error {
	port := 9222
	chrome, _ := exec.LookPath("chrome")
	if chrome == "" {
		chrome, _ = exec.LookPath("google-chrome")
	}
	if chrome == "" {
		for _, p := range []string{
			"C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		} {
			if _, err := os.Stat(p); err == nil {
				chrome = p
				break
			}
		}
	}
	if chrome == "" {
		chrome = "chrome"
	}
	cmd := exec.Command(chrome,
		"--headless", "--disable-gpu", "--no-sandbox",
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--user-data-dir="+os.TempDir()+"/ok-chrome",
	)
	cmd.Stderr = nil
	cmd.Start()
	chromeURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	for i := 0; i < 30; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, chromeURL+"/json/version", nil)
		if reqErr != nil {
			cancel()
			continue
		}
		resp, err := httpClient.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			return nil
		}
	}
	return fmt.Errorf("chrome did not start")
}

var cdpID int

func chromeCall(method string, params any) (string, error) {
	data, err := chromeCallRaw(method, params)
	if err != nil {
		return "", err
	}
	var pretty bytes.Buffer
	json.Indent(&pretty, data, "", "  ")
	return pretty.String(), nil
}

func chromeCallRaw(method string, params any) (json.RawMessage, error) {
	cdpID++
	body, err := json.Marshal(struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{ID: cdpID, Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("marshal cdp request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chromeURL+"/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new cdp request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "browser fetch close: %v\n", err)
		}
	}()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read cdp response: %w", err)
	}
	var r struct {
		Result json.RawMessage           `json:"result"`
		Error  *struct{ Message string } `json:"error"`
	}
	if json.Unmarshal(data, &r) == nil && r.Error != nil {
		return nil, fmt.Errorf("CDP: %s", r.Error.Message)
	}
	return r.Result, nil
}

func strEnum(vals ...string) map[string]any {
	m := map[string]any{"type": "string"}
	if len(vals) > 0 {
		m["enum"] = vals
	}
	return m
}
func strType() map[string]any { return map[string]any{"type": "string"} }
func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// escJS escapes s for use in a JS string context.
// Uses json.Marshal to produce a properly quoted and escaped JS string.
func escJS(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
