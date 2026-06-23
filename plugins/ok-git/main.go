// @ok/git — MCP plugin: Git operations as tools.
package main
import ("context";"encoding/json";"fmt";"os/exec";"strings";
	"github.com/NB-Agent/ok/internal/plugin")
type server struct{}
func (server) Info() (string, string) { return "ok-git", "1.0.0" }
func (server) Tools() []plugin.ToolDef { return []plugin.ToolDef{
	{Name:"git_status",Description:"Show working tree status",InputSchema:map[string]any{"type":"object","properties":map[string]any{"path":plugin.StrProp()}} },
	{Name:"git_diff",Description:"Show uncommitted changes",InputSchema:map[string]any{"type":"object","properties":map[string]any{"path":plugin.StrProp(),"staged":map[string]any{"type":"boolean"}}} },
	{Name:"git_log",Description:"Show commit history",InputSchema:map[string]any{"type":"object","properties":map[string]any{"path":plugin.StrProp(),"count":map[string]any{"type":"integer"}}} },
	{Name:"git_commit",Description:"Create a new commit",InputSchema:map[string]any{"type":"object","properties":map[string]any{"path":plugin.StrProp(),"message":plugin.StrProp()},"required":[]string{"message"}} },
	{Name:"git_branch",Description:"List or create branches",InputSchema:map[string]any{"type":"object","properties":map[string]any{"path":plugin.StrProp(),"name":plugin.StrProp(),"all":map[string]any{"type":"boolean"}}} },
} }
func (server) Call(_ context.Context, name string, args json.RawMessage) (string, error) {
	var p struct { Path, Message, Name string; Count int; Staged, All bool }
	json.Unmarshal(args, &p)
	dir := p.Path; if dir == "" { dir = "." }
	if p.Count <= 0 { p.Count = 10 }
	switch name {
	case "git_status": return gitRun(dir, "status", "--short")
	case "git_diff": if p.Staged { return gitRun(dir, "diff", "--cached") }; return gitRun(dir, "diff")
	case "git_log": return gitRun(dir, "log", "--oneline", fmt.Sprintf("-%d", p.Count))
	case "git_commit": if p.Message == "" { return "", fmt.Errorf("commit message is required") }; gitRun(dir, "add", "-A"); return gitRun(dir, "commit", "-m", p.Message)
	case "git_branch": if p.Name != "" { return gitRun(dir, "branch", p.Name) }; if p.All { return gitRun(dir, "branch", "-a") }; return gitRun(dir, "branch")
	default: return "", fmt.Errorf("unknown tool: %s", name)
	}
}
func main() { plugin.RunStdio(server{}) }
func gitRun(dir, arg string, extra ...string) (string, error) { args := append([]string{arg}, extra...); cmd := exec.Command("git", args...); cmd.Dir = dir; out, err := cmd.CombinedOutput(); return strings.TrimSpace(string(out)), err }
