package builtin

// toolGroups maps every built-in tool name to its group.
// Default (not in map) = "core" — always visible.
// Groups: core, advanced, knowledge, admin.
var toolGroups = map[string]string{
	// ── core (always visible) ──
	"bash":          "core",
	"read_file":     "core",
	"write_file":    "core",
	"edit_file":     "core",
	"multi_edit":    "core",
	"grep":          "core",
	"glob":          "core",
	"ls":            "core",
	"web_fetch":     "core",
	"todo_write":    "core",
	"complete_step": "core",
	"undo":          "core",
	"ok-verify":     "core",
	"plan":          "core",
	"run_skill":     "core",
	"install_skill": "core",
	"remember":      "core",
	"task":          "core",
	"ask":           "core",
	"tool-groups":   "core",

	// ── advanced (code/infra ops) ──
	"git":          "advanced",
	"database":     "advanced",
	"debug":        "advanced",
	"deploy":       "advanced",
	"desktop":      "advanced",
	"digest":       "advanced",
	"schedule":     "advanced",
	"workflow":     "advanced",
	"auto-heal":    "advanced",
	"bgjobs":       "advanced",
	"bash_output":  "advanced",
	"wait":         "advanced",
	"kill_shell":   "advanced",
	"browser":      "advanced",
	"computer-use": "advanced",

	// ── knowledge (intelligence/search) ──
	"rag":             "knowledge",
	"semantic-search": "knowledge",
	"symbol-find":     "knowledge",
	"style-check":     "knowledge",
	"image-read":      "knowledge",
	"video-analyze":   "knowledge",
	"ocr":             "knowledge",
	"translate":       "knowledge",

	// ── admin (system/meta) ──
	"repo":         "admin",
	"make-tool":    "admin",
	"go-profile":   "admin",
	"vuln-check":   "admin",
	"covenant":     "admin",
	"capabilities": "admin",
	"self-scan":    "admin",
	"wake-word":    "admin",
	"voice":        "admin",
	"preview":      "admin",
}

// ToolGroup returns the group for a tool name, defaulting to "core".
func ToolGroup(name string) string {
	if g, ok := toolGroups[name]; ok {
		return g
	}
	return "core"
}
