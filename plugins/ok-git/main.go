// @ok/git — MCP plugin: Git operations as tools.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	server := &mcpServer{name: "ok-git", version: "1.0.0"}
	server.run()
}

type jsonRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpServer struct {
	name    string
	version string
}

func (s *mcpServer) run() {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for dec.More() {
		var req jsonRPC
		if err := dec.Decode(&req); err != nil {
			break
		}
		resp := s.handle(context.Background(), req)
		if resp.ID != nil {
			enc.Encode(resp)
		}
	}
}

func (s *mcpServer) handle(ctx context.Context, req jsonRPC) jsonRPC {
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
				{"name": "git_status", "description": "Show working tree status", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
				{"name": "git_diff", "description": "Show uncommitted changes", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "staged": map[string]any{"type": "boolean"}}}},
				{"name": "git_log", "description": "Show commit history", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "count": map[string]any{"type": "integer"}}}},
				{"name": "git_commit", "description": "Create a new commit", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}}, "required": []string{"message"}}},
				{"name": "git_branch", "description": "List or create branches", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "all": map[string]any{"type": "boolean"}}}},
			},
		})}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &params)
		result, err := s.executeTool(ctx, params.Name, params.Arguments)
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

func (s *mcpServer) executeTool(_ context.Context, name string, args json.RawMessage) (string, error) {
	var p struct {
		Path    string `json:"path"`
		Message string `json:"message"`
		Name    string `json:"name"`
		Count   int    `json:"count"`
		Staged  bool   `json:"staged"`
		All     bool   `json:"all"`
	}
	if args != nil {
		json.Unmarshal(args, &p)
	}
	dir := p.Path
	if dir == "" {
		dir = "."
	}
	if p.Count <= 0 {
		p.Count = 10
	}

	switch name {
	case "git_status":
		return gitRun(dir, "status", "--short")
	case "git_diff":
		if p.Staged {
			return gitRun(dir, "diff", "--cached")
		}
		return gitRun(dir, "diff")
	case "git_log":
		return gitRun(dir, "log", "--oneline", fmt.Sprintf("-%d", p.Count))
	case "git_commit":
		if p.Message == "" {
			return "", fmt.Errorf("commit message is required")
		}
		gitRun(dir, "add", "-A")
		return gitRun(dir, "commit", "-m", p.Message)
	case "git_branch":
		if p.Name != "" {
			return gitRun(dir, "branch", p.Name)
		}
		if p.All {
			return gitRun(dir, "branch", "-a")
		}
		return gitRun(dir, "branch")
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func gitRun(dir, arg string, extra ...string) (string, error) {
	args := append([]string{arg}, extra...)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
