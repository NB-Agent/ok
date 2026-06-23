// @ok/desktop — MCP plugin: Desktop automation via OS commands (Windows).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	s := &mcpServer{name: "ok-desktop", version: "1.0.0"}
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
		"name": "desktop", "description": "Operate the computer",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"action": strEnum("screenshot", "processes", "kill", "start", "clipboard-read", "clipboard-write",
				"windows-list", "focus-window", "send-keys", "sleep", "mouse-move", "mouse-click",
				"mouse-double-click", "mouse-right-click", "scroll", "notify"),
			"target": strType(), "text": strType(), "path": strType(),
			"x": map[string]any{"type": "number"}, "y": map[string]any{"type": "number"},
			"duration_ms": map[string]any{"type": "integer"},
			"headline":    strType(),
		}, "required": []string{"action"}},
	}}}
}

type desktopArgs struct {
	Action     string  `json:"action"`
	Target     string  `json:"target"`
	Text       string  `json:"text"`
	Path       string  `json:"path"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	DurationMs int     `json:"duration_ms"`
	Headline   string  `json:"headline"`
}

func (s *mcpServer) exec(name string, args json.RawMessage) (string, error) {
	if name != "desktop" {
		return "", fmt.Errorf("unknown: %s", name)
	}
	var p desktopArgs
	json.Unmarshal(args, &p)

	switch p.Action {
	case "screenshot":
		path := p.Path
		if path == "" {
			path = os.TempDir() + "\\ok-screen.png"
		}
		return ps(fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; $b=New-Object Drawing.Bitmap([Windows.Forms.Screen]::PrimaryScreen.Bounds.Width,[Windows.Forms.Screen]::PrimaryScreen.Bounds.Height); $g=[Drawing.Graphics]::FromImage($b); $g.CopyFromScreen(0,0,0,0,$b.Size); $b.Save(%s)`, escPS(path)))
	case "processes":
		return ps("Get-Process | Format-Table -AutoSize | Out-String -Width 4096")
	case "kill":
		return ps(fmt.Sprintf("Stop-Process -Name %s -Force", escPS(p.Target)))
	case "start":
		return ps(fmt.Sprintf("Start-Process %s", escPS(p.Path)))
	case "clipboard-read":
		return ps("Get-Clipboard")
	case "clipboard-write":
		return ps(fmt.Sprintf("Set-Clipboard -Value %s", escPS(p.Text)))
	case "windows-list":
		return ps("Get-Process | Where-Object MainWindowTitle | Select-Object Name,Id,MainWindowTitle | Format-Table -AutoSize | Out-String -Width 4096")
	case "send-keys":
		return ps(fmt.Sprintf("[System.Windows.Forms.SendKeys]::SendWait(%s)", escPS(p.Text)))
	case "sleep":
		return ps(fmt.Sprintf("Start-Sleep -Milliseconds %d", p.DurationMs))
	case "mouse-move":
		return ps(fmt.Sprintf("[System.Windows.Forms.Cursor]::Position = New-Object Drawing.Point(%d,%d)", int(p.X), int(p.Y)))
	case "mouse-click":
		return ps(fmt.Sprintf("Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Cursor]::Position = New-Object Drawing.Point(%d,%d); [System.Windows.Forms.SendKeys]::SendWait('{Click}')", int(p.X), int(p.Y)))
	case "focus-window":
		if !isValidWindowTarget(p.Target) {
			return "", fmt.Errorf("invalid window target: %s", p.Target)
		}
		// Use -like (glob) instead of -match (regex) to avoid regex injection.
		return ps(fmt.Sprintf("$h=(Get-Process | Where-Object {$_.MainWindowTitle -like %s}).MainWindowHandle; Add-Type -Name W -Member '[DllImport(\"user32.dll\")]public static extern bool SetForegroundWindow(IntPtr hWnd)'; [W]::SetForegroundWindow($h)", escPS(p.Target)))
	case "notify":
		return ps(fmt.Sprintf("New-BurntToastNotification -Text %s,%s 2>$null; Write-Output 'ok'", escPS(p.Headline), escPS(p.Text)))
	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}
}

func ps(cmd string) (string, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-Command", cmd).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// escPS escapes s for use inside a PowerShell single-quoted string ('...').
func escPS(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		return r
	}, s)
	return "'" + s + "'"
}

// isValidWindowTarget rejects characters unsafe for PowerShell -like matching.
func isValidWindowTarget(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == '[' || r == ']' || r == '*' || r == '?' || r == '`' || r == '$' || r == '(' || r == ')' || r == '{' || r == '}' || r == ';' || r == '|' || r == '&' {
			return false
		}
	}
	return true
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
