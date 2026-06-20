// @ok/wake-word — MCP plugin: Wake word detection.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

func main() {
	s := &server{running: &sync.Map{}}
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
type server struct{ running *sync.Map }

var schema = json.RawMessage(`{"tools":[{"name":"wake-word","description":"Listen for wake word","inputSchema":{"type":"object","properties":{"action":{"type":"string","enum":["start","stop","status"]},"keyword":{"type":"string"},"timeout_sec":{"type":"integer"}},"required":["action"]}}]}`)

func (s *server) handle(req msg) msg {
	id := req.ID
	switch req.Method {
	case "initialize":
		return msg{JSONRPC: "2.0", ID: id, Result: raw(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"ok-wake-word","version":"1.0.0"},"capabilities":{"tools":{}}}`)}
	case "tools/list":
		return msg{JSONRPC: "2.0", ID: id, Result: schema}
	case "tools/call":
		var params struct {
			Name      string
			Arguments json.RawMessage
		}
		json.Unmarshal(req.Params, &params)
		if params.Name != "wake-word" {
			return msg{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: -32000, Message: "unknown tool"}}
		}
		var p struct {
			Action, Keyword string
			Timeout         int
		}
		json.Unmarshal(params.Arguments, &p)

		switch p.Action {
		case "start":
			if p.Keyword == "" {
				p.Keyword = "hey ok"
			}
			s.running.Store("listener", true)
			if p.Timeout > 0 {
				go func() {
					defer func() {
						if r := recover(); r != nil {
							fmt.Fprintf(os.Stderr, "wake-word listener panic: %v\n", r)
						}
					}()
					time.Sleep(time.Duration(p.Timeout) * time.Second)
					s.running.Store("listener", false)
				}()
			}
			exec.Command("porcupine_demo", "--keywords", p.Keyword).Start()
			return msg{JSONRPC: "2.0", ID: id, Result: raw(`{"content":[{"type":"text","text":"` + p.Keyword + `"}]}`)}
		case "stop":
			s.running.Store("listener", false)
			exec.Command("taskkill", "/f", "/im", "porcupine_demo.exe").Run()
			return msg{JSONRPC: "2.0", ID: id, Result: raw(`{"content":[{"type":"text","text":"stopped"}]}`)}
		case "status":
			status := "STOPPED"
			if v, ok := s.running.Load("listener"); ok {
				if b, okb := v.(bool); okb && b {
					status = "RUNNING"
				}
			}
			return msg{JSONRPC: "2.0", ID: id, Result: raw(`{"content":[{"type":"text","text":"` + status + `"}]}`)}
		}
	}
	return msg{JSONRPC: "2.0", ID: id}
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }
