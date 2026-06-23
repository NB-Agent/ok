// @ok/voice — MCP plugin: Speech and audio interaction via OS commands.
package main
import ("context";"encoding/base64";"encoding/json";"fmt";"os/exec";"runtime";"strings";
	"github.com/NB-Agent/ok/internal/plugin")
type server struct{}
func (server) Info() (string, string) { return "ok-voice", "1.0.0" }
func (server) Tools() []plugin.ToolDef { return []plugin.ToolDef{{Name:"voice",Description:"Speak text or listen for voice input",InputSchema:map[string]any{"type":"object","properties":map[string]any{"action":plugin.StrEnum("speak","listen","converse"),"text":plugin.StrProp()},"required":[]string{"action"}} }} }
func (s server) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	if name != "voice" { return "", fmt.Errorf("unknown: %s", name) }
	var p struct{ Action, Text string }; json.Unmarshal(args, &p)
	switch p.Action {
	case "speak": return speak(p.Text)
	case "listen": return listen()
	case "converse": return s.converse(p.Text)
	}
	return "", fmt.Errorf("unknown action: %s", p.Action)
}
func main() { plugin.RunStdio(server{}) }
func speak(text string) (string, error) {
	if text == "" { return "", fmt.Errorf("text required") }
	switch runtime.GOOS {
	case "windows":
		// Use base64 to avoid PowerShell injection through single quotes.
		b64 := base64.StdEncoding.EncodeToString([]byte(text))
		cmd := exec.Command("powershell","-NoProfile","-Command",
			fmt.Sprintf(`$t=[System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String('%s'));Add-Type -AssemblyName System.Speech;(New-Object System.Speech.Synthesis.SpeechSynthesizer).Speak($t)`,b64))
		return run(cmd)
	case "darwin": return run(exec.Command("say","--",text))
	default: return run(exec.Command("espeak","--",text))
	}
}
func listen() (string, error) {
	switch runtime.GOOS {
	case "windows": return run(exec.Command("powershell","-Command",`Add-Type -AssemblyName System.Speech; $r=New-Object System.Speech.Recognition.SpeechRecognizer; $r.SetInputToDefaultAudioDevice(); $r.Recognize() | Select-Object -ExpandProperty Text`))
	default: return "", fmt.Errorf("audio input not supported on %s", runtime.GOOS)
	}
}
func (s server) converse(text string) (string, error) {
	if text == "" { spoken, err := listen(); if err != nil { return "", err }; text = spoken }
	speak("I received: " + text); return fmt.Sprintf("conversed: %s", text), nil
}
func run(cmd *exec.Cmd) (string, error) { out, err := cmd.CombinedOutput(); return strings.TrimSpace(string(out)), err }
