// @ok/voice — MCP plugin: Speech and audio interaction via OS commands.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func main() {
	s := &mcpServer{name: "ok-voice", version: "1.0.0"}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for dec.More() {
		var req jsonRPC
		dec.Decode(&req)
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
	Code    int
	Message string
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
	}
	return jsonRPC{JSONRPC: "2.0", ID: id}
}

func schemas() map[string]any {
	return map[string]any{"tools": []map[string]any{{
		"name": "voice", "description": "Speak text or listen for voice input",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"action": strEnum("speak", "listen", "converse"),
			"text":   strType(),
		}, "required": []string{"action"}},
	}}}
}

func (s *mcpServer) exec(name string, args json.RawMessage) (string, error) {
	if name != "voice" {
		return "", fmt.Errorf("unknown: %s", name)
	}
	var p struct{ Action, Text string }
	json.Unmarshal(args, &p)
	switch p.Action {
	case "speak":
		return speak(p.Text)
	case "listen":
		return listen()
	case "converse":
		return s.converse(p.Text)
	}
	return "", fmt.Errorf("unknown action: %s", p.Action)
}

func speak(text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("text required")
	}
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("powershell", "-Command",
			fmt.Sprintf("Add-Type -AssemblyName System.Speech; (New-Object System.Speech.Synthesis.SpeechSynthesizer).Speak('%s')", strings.ReplaceAll(text, "'", "''")))
		return run(cmd)
	case "darwin":
		return run(exec.Command("say", text))
	default:
		return run(exec.Command("espeak", text))
	}
}

func listen() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return run(exec.Command("powershell", "-Command",
			`Add-Type -AssemblyName System.Speech; $r=New-Object System.Speech.Recognition.SpeechRecognizer; $r.SetInputToDefaultAudioDevice(); $r.Recognize() | Select-Object -ExpandProperty Text`))
	default:
		return "", fmt.Errorf("audio input not supported on %s", runtime.GOOS)
	}
}

func (s *mcpServer) converse(text string) (string, error) {
	if text == "" {
		spoken, err := listen()
		if err != nil {
			return "", err
		}
		text = spoken
	}
	speak("I received: " + text)
	return fmt.Sprintf("conversed: %s", text), nil
}

func run(cmd *exec.Cmd) (string, error) {
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
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
