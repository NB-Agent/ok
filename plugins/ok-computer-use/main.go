// @ok/computer-use — MCP plugin: Visual desktop automation.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	s := &mcpServer{name: "ok-computer-use", version: "1.0.0"}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for dec.More() {
		var req msg
		dec.Decode(&req)
		resp := s.handle(req)
		if resp.ID != nil {
			enc.Encode(resp)
		}
	}
}

type msg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}
type rpcErr struct {
	Code    int
	Message string
}
type mcpServer struct{ name, version string }

func (s *mcpServer) handle(req msg) msg {
	id := req.ID
	switch req.Method {
	case "initialize":
		return msg{JSONRPC: "2.0", ID: id, Result: toJSON(map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": s.name, "version": s.version},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		})}
	case "tools/list":
		return msg{JSONRPC: "2.0", ID: id, Result: toJSON(map[string]any{"tools": []map[string]any{{
			"name":        "computer-use",
			"description": "Control the computer visually — screenshot, analyze, act",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"goal": map[string]any{"type": "string"},
			}, "required": []string{"goal"}},
		}}})}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &params)
		if params.Name != "computer-use" {
			return msg{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: -32000, Message: "unknown tool"}}
		}
		var p struct{ Goal string }
		json.Unmarshal(params.Arguments, &p)
		if p.Goal == "" {
			return msg{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: -32000, Message: "goal required"}}
		}
		ss, _ := screenshot()
		return msg{JSONRPC: "2.0", ID: id, Result: toJSON(map[string]any{
			"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("Goal: %s\nScreenshot: %s\n", p.Goal, ss)}},
		})}
	}
	return msg{JSONRPC: "2.0", ID: id}
}

func screenshot() (string, error) {
	path := os.TempDir() + "\\ok-cu.png"
	err := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; $b=New-Object Drawing.Bitmap([Windows.Forms.Screen]::PrimaryScreen.Bounds.Width,[Windows.Forms.Screen]::PrimaryScreen.Bounds.Height); $g=[Drawing.Graphics]::FromImage($b); $g.CopyFromScreen(0,0,0,0,$b.Size); $b.Save('%s')`, path)).Run()
	return path, err
}

func toJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
