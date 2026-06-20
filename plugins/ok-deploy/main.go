// @ok/deploy — MCP plugin: SSH-based deployment.
package main

import (
	"encoding/json"
	"os"
	"os/exec"
)

func main() {
	s := &server{}
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
type server struct{}

var schema = json.RawMessage(`{"tools":[{"name":"deploy","description":"Deploy to remote servers via SSH","inputSchema":{"type":"object","properties":{"action":{"type":"string","enum":["status","ssh","health","list-targets","build","upload","restart","full-deploy","dry-run"]},"target":{"type":"string"},"command":{"type":"string"},"service":{"type":"string"},"os":{"type":"string"},"arch":{"type":"string"},"binary":{"type":"string"}},"required":["action"]}}]}`)

func (s *server) handle(req msg) msg {
	id := req.ID
	switch req.Method {
	case "initialize":
		return msg{JSONRPC: "2.0", ID: id, Result: raw(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"ok-deploy","version":"1.0.0"},"capabilities":{"tools":{}}}`)}
	case "tools/list":
		return msg{JSONRPC: "2.0", ID: id, Result: schema}
	case "tools/call":
		var params struct {
			Name      string
			Arguments json.RawMessage
		}
		json.Unmarshal(req.Params, &params)
		if params.Name != "deploy" {
			return msg{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: -32000, Message: "unknown tool"}}
		}
		var p struct{ Action, Target, Command, Service, OS, Arch, Binary string }
		json.Unmarshal(params.Arguments, &p)
		r := s.exec(&p)
		return msg{JSONRPC: "2.0", ID: id, Result: raw(`{"content":[{"type":"text","text":` + esc(r) + `}]}`)}
	}
	return msg{JSONRPC: "2.0", ID: id}
}

func (s *server) exec(p *struct{ Action, Target, Command, Service, OS, Arch, Binary string }) string {
	switch p.Action {
	case "build":
		r, _ := exec.Command("go", "build", "-o", p.Binary, ".").CombinedOutput()
		return string(r)
	case "ssh":
		r, _ := exec.Command("ssh", p.Target, p.Command).CombinedOutput()
		return string(r)
	case "upload":
		r, _ := exec.Command("scp", p.Binary, p.Target+":~/").CombinedOutput()
		return string(r)
	case "restart":
		if !isValidServiceName(p.Service) {
			return "error: invalid service name: " + esc(p.Service)
		}
		r, _ := exec.Command("ssh", p.Target, "sudo systemctl restart "+p.Service).CombinedOutput()
		return string(r)
	case "health":
		if !isValidServiceName(p.Service) {
			return "error: invalid service name: " + esc(p.Service)
		}
		r, _ := exec.Command("ssh", p.Target, "systemctl is-active "+p.Service).CombinedOutput()
		return string(r)
	default:
		return "ok-deploy v1.0.0"
	}
}

func esc(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// isValidServiceName validates a systemd service name: only letters, digits,
// hyphens, dots, and underscores allowed — no shell metacharacters.
func isValidServiceName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_') {
			return false
		}
	}
	return true
}
