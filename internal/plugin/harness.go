package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// ToolDef describes one tool the plugin exposes.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// StdioServer is the contract every MCP stdio plugin satisfies.
type StdioServer interface {
	Info() (name, version string)
	Tools() []ToolDef
	Call(ctx context.Context, name string, args json.RawMessage) (string, error)
}

// MustJSON marshals v to json.RawMessage, panicking on error.
func MustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("plugin: marshal: %v", err))
	}
	return b
}

// StrEnum returns a JSON Schema string property with enum values.
func StrEnum(vals ...string) map[string]any {
	return map[string]any{"type": "string", "enum": vals}
}

// StrProp returns a plain {"type":"string"} JSON Schema property.
func StrProp() map[string]any { return map[string]any{"type": "string"} }

// RunStdio runs the JSON-RPC stdio loop for an MCP plugin.
func RunStdio(s StdioServer) {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	name, version := s.Info()
	for dec.More() {
		var req jrpc
		if err := dec.Decode(&req); err != nil {
			break
		}
		resp := dispatchStdio(name, version, s, req)
		if resp.ID != nil {
			_ = enc.Encode(resp)
		}
	}
}

type jrpc struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func dispatchStdio(name, version string, s StdioServer, req jrpc) jrpc {
	id := req.ID
	switch req.Method {
	case "initialize":
		return jrpc{
			JSONRPC: "2.0",
			ID:      id,
			Result: MustJSON(map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]any{"name": name, "version": version},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			}),
		}
	case "tools/list":
		return jrpc{
			JSONRPC: "2.0",
			ID:      id,
			Result:  MustJSON(map[string]any{"tools": s.Tools()}),
		}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &params)
		result, err := s.Call(context.Background(), params.Name, params.Arguments)
		if err != nil {
			return jrpc{
				JSONRPC: "2.0",
				ID:      id,
				Error:   &rpcError{Code: -32000, Message: err.Error()},
			}
		}
		return jrpc{
			JSONRPC: "2.0",
			ID:      id,
			Result: MustJSON(map[string]any{
				"content": []map[string]any{{"type": "text", "text": result}},
			}),
		}
	default:
		return jrpc{JSONRPC: "2.0", ID: id}
	}
}
