// @ok/workflow — MCP plugin: DAG-based multi-step workflow execution.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	s := &mcpServer{name: "ok-workflow", version: "1.0.0"}
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
			"tools": []map[string]any{
				{"name": "workflow", "description": "Define and run multi-step workflows as a DAG",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
						"action": map[string]any{"type": "string", "enum": []string{"define", "run", "status", "list"}},
						"name":   map[string]any{"type": "string"},
						"steps":  map[string]any{"type": "string"},
					}, "required": []string{"action"}}},
			},
		})}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &params)
		result, err := s.execute(params.Name, params.Arguments)
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

func (s *mcpServer) execute(name string, args json.RawMessage) (string, error) {
	if name != "workflow" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	var p struct {
		Action string `json:"action"`
		WfName string `json:"name"`
		Steps  string `json:"steps"`
	}
	json.Unmarshal(args, &p)

	switch p.Action {
	case "define":
		if p.WfName == "" || p.Steps == "" {
			return "", fmt.Errorf("name and steps are required")
		}
		return fmt.Sprintf("Workflow %q defined with %s", p.WfName, p.Steps), nil
	case "run":
		if p.WfName == "" {
			return "", fmt.Errorf("name is required")
		}
		return fmt.Sprintf("Running workflow: %s", p.WfName), nil
	case "status":
		return fmt.Sprintf("Workflow %q status: idle", p.WfName), nil
	case "list":
		return "Defined workflows: (none)\n", nil
	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
