// @ok/repo — MCP plugin: Multi-repository management.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	s := &mcpServer{name: "ok-repo", version: "1.0.0"}
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

var repos = map[string]string{} // name → abs path

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
				"name": "repo", "description": "Manage multiple repositories",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
					"action":  map[string]any{"type": "string", "enum": []string{"add", "list", "switch", "run", "info", "remove"}},
					"name":    map[string]any{"type": "string"},
					"path":    map[string]any{"type": "string"},
					"command": map[string]any{"type": "string"},
				}, "required": []string{"action"}},
			}},
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
	if name != "repo" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	var p struct {
		Action  string `json:"action"`
		Name    string `json:"name"`
		Path    string `json:"path"`
		Command string `json:"command"`
	}
	json.Unmarshal(args, &p)

	switch p.Action {
	case "add":
		if p.Name == "" {
			return "", fmt.Errorf("name is required")
		}
		absPath := p.Path
		if absPath == "" {
			absPath, _ = os.Getwd()
		}
		absPath, _ = filepath.Abs(absPath)
		repos[p.Name] = absPath
		return fmt.Sprintf("added repo %q → %s", p.Name, absPath), nil

	case "list":
		if len(repos) == 0 {
			return "No repos registered.\n", nil
		}
		var b strings.Builder
		b.WriteString("Registered repos:\n")
		for name, path := range repos {
			b.WriteString(fmt.Sprintf("  %s → %s\n", name, path))
		}
		return b.String(), nil

	case "switch":
		path, ok := repos[p.Name]
		if !ok {
			return "", fmt.Errorf("repo %q not found", p.Name)
		}
		if err := os.Chdir(path); err != nil {
			return "", err
		}
		return fmt.Sprintf("switched to %q (%s)", p.Name, path), nil

	case "run":
		path, ok := repos[p.Name]
		if !ok {
			return "", fmt.Errorf("repo %q not found", p.Name)
		}
		if p.Command == "" {
			return "", fmt.Errorf("command is required")
		}
		return runCmdDir(path, p.Command)

	case "info":
		path, ok := repos[p.Name]
		if !ok {
			return "", fmt.Errorf("repo %q not found", p.Name)
		}
		return runCmdDir(path, "git", "log", "--oneline", "-5")

	case "remove":
		if _, ok := repos[p.Name]; !ok {
			return "", fmt.Errorf("repo %q not found", p.Name)
		}
		delete(repos, p.Name)
		return fmt.Sprintf("removed repo %q", p.Name), nil

	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}

func runCmdDir(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
