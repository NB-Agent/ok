// @ok/database — MCP plugin: SQLite/PostgreSQL/MySQL via CLI.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	s := &mcpServer{name: "ok-database", version: "1.0.0"}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	var req request
	for dec.More() {
		if err := dec.Decode(&req); err != nil {
			break
		}
		resp := s.handle(req)
		if resp.ID != nil {
			enc.Encode(resp)
		}
	}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int
	Message string
}
type mcpServer struct{ name, version string }

func (s *mcpServer) handle(req request) response {
	id := req.ID
	switch req.Method {
	case "initialize":
		return response{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": s.name, "version": s.version},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		})}
	case "tools/list":
		return response{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"tools": []map[string]any{{
				"name": "database", "description": "Query SQLite/PostgreSQL/MySQL databases",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
					"driver": strEnum("sqlite3", "postgres", "mysql"),
					"dsn":    strType(),
					"query":  strType(),
				}, "required": []string{"dsn", "query"}},
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
			return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32000, Message: err.Error()}}
		}
		return response{JSONRPC: "2.0", ID: id, Result: mustJSON(map[string]any{
			"content": []map[string]any{{"type": "text", "text": result}},
		})}
	}
	return response{JSONRPC: "2.0", ID: id}
}

func (s *mcpServer) exec(name string, args json.RawMessage) (string, error) {
	if name != "database" {
		return "", fmt.Errorf("unknown: %s", name)
	}
	var p struct{ Driver, DSN, Query string }
	json.Unmarshal(args, &p)
	if p.DSN == "" || p.Query == "" {
		return "", fmt.Errorf("dsn and query required")
	}
	return s.queryDB(p.Driver, p.DSN, p.Query)
}

func (s *mcpServer) queryDB(driver, dsn, q string) (string, error) {
	if hasMultipleStatements(q) {
		return "", fmt.Errorf("multiple SQL statements are not allowed for security reasons")
	}
	switch driver {
	case "sqlite3":
		out, err := exec.Command("sqlite3", dsn, "-json", q).Output()
		if err != nil {
			return "", fmt.Errorf("sqlite3: %w", err)
		}
		var buf bytes.Buffer
		json.Indent(&buf, out, "", "  ")
		return buf.String(), nil
	case "postgres":
		out, err := exec.Command("psql", dsn, "-c", q).Output()
		return strings.TrimSpace(string(out)), err
	case "mysql":
		out, err := exec.Command("mysql", "-e", q).Output()
		return strings.TrimSpace(string(out)), err
	}
	return "", fmt.Errorf("unsupported driver: %s", driver)
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

// hasMultipleStatements detects SQL injection by checking for multiple
// statements (semicolons outside of single-quoted string literals).
func hasMultipleStatements(query string) bool {
	inString := false
	for i := 0; i < len(query); i++ {
		ch := query[i]
		if ch == '\'' {
			inString = !inString
			continue
		}
		if ch == ';' && !inString {
			return true
		}
	}
	return false
}
