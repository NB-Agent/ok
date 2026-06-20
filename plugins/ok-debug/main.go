// @ok/debug — MCP plugin: Delve (dlv) Go debugger.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	s := &mcpServer{name: "ok-debug", version: "1.0.0"}
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
			"tools": []map[string]any{addSchema("debug", "Debug Go programs using Delve (dlv)", map[string]any{
				"action": strEnum("start", "break", "continue", "next", "step", "print", "stack", "locals", "list", "restart", "stop", "set"),
				"target": strType(),
				"args":   strType(),
			}, []string{"action"})},
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

func addSchema(name, desc string, props map[string]any, required []string) map[string]any {
	return map[string]any{
		"name": name, "description": desc,
		"inputSchema": map[string]any{"type": "object", "properties": props, "required": required},
	}
}
func strEnum(vals ...string) map[string]any { return map[string]any{"type": "string", "enum": vals} }
func strType() map[string]any               { return map[string]any{"type": "string"} }

func (s *mcpServer) exec(name string, args json.RawMessage) (string, error) {
	if name != "debug" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	var p struct {
		Action   string `json:"action"`
		Target   string `json:"target"`
		ProgArgs string `json:"args"`
	}
	json.Unmarshal(args, &p)
	switch p.Action {
	case "start":
		return runCmd("debug", "--headless", "-l", "127.0.0.1:0", p.Target)
	case "break":
		return runCmd("break", p.Target)
	case "continue":
		return runCmd("continue")
	case "next":
		return runCmd("next")
	case "step":
		return runCmd("step")
	case "print":
		return runCmd("print", p.Target)
	case "stack":
		return runCmd("stack")
	case "locals":
		return runCmd("locals")
	case "list":
		return runCmd(append([]string{"list"}, splitArg(p.Target)...)...)
	case "restart":
		return runCmd("restart")
	case "stop":
		return runCmd("stop")
	case "set":
		return runCmd("set", p.Target)
	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}

func runCmd(args ...string) (string, error) {
	out, err := exec.Command("dlv", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func splitArg(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
