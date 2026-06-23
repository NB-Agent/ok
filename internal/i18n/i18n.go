// Package i18n holds the CLI's translatable strings and a small detection
// helper.
//
// Architecture: a single Messages struct of exported string fields
// (plain text or fmt format strings, suffix *Fmt flags the latter). Each
// language declares one Messages value in its own file. Call sites read
// i18n.M.SomeField; for parameterised messages they pass it to fmt.Sprintf.
//
// Adding a field requires updating every messages_*.go file — drift is caught
// at test time by TestCatalogsComplete via reflection, so a missing translation
// fails CI instead of surfacing as a blank line at runtime.
//
// Scope (v1): CLI surface only — welcome, init wizard, chat REPL banner, usage,
// user-facing CLI errors. System prompts, internal error wrappers, and agent
// runtime telemetry stay English so model behavior and developer logs are
// language-stable.
//
// Language resolution uses a chain-of-responsibility:
//  1. Precompiled resolver — compiled-in Messages catalogs (30+ languages)
//  2. Live resolver — runtime LLM translation for all other languages
//  3. English fallback — always works
//
// New languages register via registerLanguage() in their init().
//
//go:generate go run gen.go
package i18n

import (
	"os"
	"strings"
)

// catalogs stores all compiled language catalogs keyed by tag.
// Register new languages via registerLanguage().
var catalogs = map[string]*Messages{
	"en": &English,
	"zh": &Chinese,
	"ja": &Japanese,
	"ko": &Korean,
}

// languageAliases maps language tags to their known name aliases
// so normalize() can match "Chinese (China)" → "zh" without hardcoding.
var languageAliases = map[string][]string{
	"zh": {"chinese", "中文"},
	"ja": {"japanese", "日本語"},
	"ko": {"korean", "한국어"},
	"hi": {"hindi", "हिन्दी"},
	"es": {"spanish", "español"},
	"pt": {"portuguese", "português"},
	"ru": {"russian", "русский"},
	"ar": {"arabic", "العربية"},
	"fr": {"french", "français"},
	"id": {"indonesian", "bahasa indonesia"},
	"de": {"german", "deutsch"},
	"vi": {"vietnamese", "tiếng việt"},
	"th": {"thai", "ไทย"},
	"tr": {"turkish", "türkçe"},
	"pl": {"polish", "polski"},
	"nl": {"dutch", "nederlands"},
	"it": {"italian", "italiano"},
	"ta": {"tamil", "தமிழ்"},
	"bn": {"bengali", "বাংলা"},
	"ms": {"malay", "bahasa melayu"},
	"tl": {"filipino"},
	"sw": {"swahili", "kiswahili"},
	"ha": {"hausa"},
	"zu": {"zulu", "iszulu"},
	"ur": {"urdu", "اردو"},
	"fa": {"persian", "فارسی"},
	"ro": {"romanian", "română"},
	"uk": {"ukrainian", "українська"},
	"el": {"greek", "ελληνικά"},
}

// registerLanguage adds a language catalog to the global registry.
// Called from init() in each messages_xx.go file (hand-written or generated).
//
//nolint:unused
func registerLanguage(tag string, m *Messages) {
	catalogs[tag] = m
}

// Messages is the catalog of translatable CLI strings. Plain fields are
// printed verbatim; *Fmt fields are fmt format strings the caller passes to
// fmt.Sprintf. Catalog values do not include trailing newlines — call sites
// add framing whitespace, so the same field works wherever it appears.
type Messages struct {
	// welcome / status screen
	Subtitle        string // tagline under the product name in the welcome box
	WelcomeTitleFmt string // first-run box title — %s = product name (styled)
	NoConfigYet     string // first-run cue under the welcome box
	StartingChatFmt string // "Starting %s…" before dropping into chat
	SetKeyHint      string // shown when key is missing after init
	ConfigLabel     string // "config" status row label
	ModelsLabel     string // "models" status row label
	ConfigNotFound  string // shown when no config file exists
	ConfigErrorFmt  string // "%s — error: %v" — config path + parse error
	NoKey           string // status dot — no API key set
	Ready           string // status dot — provider ready
	GetStarted      string // section title above numbered steps
	StepScaffold    string // step 1 desc — ok setup
	StepSetKey      string // step 2 command label

	// `ok init` — points to the in-session /init skill + setup
	InitHint       string
	StepSetKeyHint string // step 2 desc — env var hint
	StepChatDesc   string // ok chat step desc
	StepRunDesc    string // ok run step desc
	HelpFooter     string // dim footer linking to ok help

	// chat REPL
	ChatTip           string // tip line under the chat banner
	TurnCancelled     string // shown when Ctrl-C aborts the in-flight turn but the chat keeps running
	NoSessionToResume string // shown when --continue / --resume finds nothing
	ResumeRequiresTTY string // shown when --resume runs piped instead of on a terminal
	PickSessionLabel  string // header on the --resume picker

	// chat TUI status line / approval banner.
	ChatStatusThinkingFmt  string // "%s thinking… (%ds · <cancel hint>)" — %s = spinner, %d = elapsed s
	ChatStatusIdle         string // shortcuts hint when idle
	ChatStatusPlanApproval string // shortcuts hint while a plan is pending
	PlanApprovalPrompt     string // one-line "plan above is ready" banner shown above the input
	ChatStatusToolApproval string // shortcuts hint while a tool call awaits approval
	ToolApprovalPromptFmt  string // "Allow %s%s?" banner — %s = tool name, %s = subject (leading space, or empty)

	// `ask` tool question card.
	AskTypeSomething   string // the "type your own answer" option label
	AskTypingHint      string // shown on that row while entering free text
	AskChatInstead     string // the "don't pick, just chat" option label
	ChatStatusQuestion string // shortcuts hint while a question card is open

	// chat TUI slash commands.
	SlashCompactDone   string // "/compact" succeeded
	SlashCompactFailed string // "/compact" errored, prefixed before the underlying error
	SlashNewDone       string // "/new" succeeded
	SlashNewFailed     string // "/new" errored
	SlashTodoCleared   string // "/todo" dismissed the pinned task list
	SlashUnavailable   string // the command is configured off (no callback wired)
	SlashUnknown       string // shown when the user types an unrecognized "/cmd"
	SlashHelp          string // listed commands
	SlashPromptEmpty   string // an MCP prompt returned no text to send
	SlashMCPNone       string // /mcp when no MCP servers are connected
	CompHintSlash      string // key hint footer under the slash-command menu
	CompHintFile       string // key hint footer under the @ file/resource menu

	// slash command + sub-command descriptions shown in the menu (CLI and desktop
	// share these via i18n.M, so both frontends localize identically).
	CmdNew          string // /new
	CmdCompact      string // /compact
	CmdModel        string // /model
	CmdMemory       string // /memory
	CmdMcp          string // /mcp
	CmdHooks        string // /hooks
	CmdSkill        string // /skill
	CmdAudit        string // /audit
	CmdSearch       string // /search
	CmdHelp         string // /help
	CmdTodo         string // /todo
	ArgSkillList    string // /skill list
	ArgSkillShow    string // /skill show
	ArgSkillNew     string // /skill new
	ArgSkillPaths   string // /skill paths
	ArgMcpAdd       string // /mcp add
	ArgMcpRemove    string // /mcp remove
	ArgMcpList      string // /mcp list
	ArgMcpConnected string // /mcp remove <server> tag
	ArgHooksList    string // /hooks list
	ArgHooksTrust   string // /hooks trust
	ArgModelCurrent string // /model <ref> active tag

	// management listing notices (the Submit path: desktop / HTTP frontends)
	ListModelsHeaderFmt string // "models (active: %s)"
	ListModelsHint      string // how to switch
	ListMemoryHeader    string // "memory files"
	ListMemoryNone      string // no memory docs
	ListSkillsHeaderFmt string // "skills (%d)"
	ListSkillsNone      string // no skills
	ListHooksHeaderFmt  string // "hooks (%d active)"
	ListHooksNone       string // no hooks
	ListMcpHeader       string // "mcp servers"
	ListMcpNone         string // no mcp servers

	// init wizard
	SelectProvidersLabel  string // multi-select label
	EnterAPIKeysHeader    string // header before the per-env-var prompts
	MissingKeyIntro       string // shown when re-running the key step on a configured setup
	WroteFileFmt          string // "Wrote %s" — used for ok.toml and .env both
	SetupComplete         string // success line at end of init
	SetupCancelled        string // shown when the user aborts the wizard
	TryHintFmt            string // "Try: %s" — %s = command to try (styled)
	NextHint              string // non-interactive post-write hint
	ConfirmReconfigureFmt string // "%s already exists. Reconfigure and overwrite?"
	KeepingExisting       string // when the user declines to overwrite
	NotOverwritingFmt     string // non-interactive overwrite refusal

	// top-level / runAgent
	UnknownCommandFmt string // "unknown command %q"
	UsageRunHint      string // "usage: ok run [--model NAME] <task>"
	ErrorPrefix       string // "error:" — prefix for fatal-error output
	WriteConfigErr    string // "write config:" — prefix for write failure
	WriteEnvErr       string // "write .env:" — prefix for env-write failure

	// selection menus
	SelectOneHint  string // "(↑/↓ · Enter · q to cancel)"
	SelectManyHint string // "(↑/↓ · Space · Enter · q)"

	// usage / help
	UsageBody string // full multi-line help text
}

// M is the active catalog. DetectLanguage replaces it; English is the
// default so any code path that runs before detection still has text.
var M = English

// DetectLanguage selects a catalog from override (e.g. cfg.Language) or the
// environment and installs it as M. Returns the resolved tag ("en", "zh") so
// callers can log or expose it.
//
// Priority: override > OK_LANG > LC_ALL > LC_MESSAGES > LANG > "en".
func DetectLanguage(override string) string {
	for _, c := range append([]string{override}, envCandidates()...) {
		if tag := normalize(c); tag != "" {
			return setLanguage(tag)
		}
	}
	return setLanguage("en")
}

func envCandidates() []string {
	keys := []string{"OK_LANG", "LC_ALL", "LC_MESSAGES", "LANG"}
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = os.Getenv(k)
	}
	return out
}

func setLanguage(tag string) string {
	if m, ok := catalogs[tag]; ok {
		M = *m
		return tag
	}
	M = English
	return "en"
}

// normalize maps a locale string (e.g. "zh_CN.UTF-8", "zh-Hans-CN", "Chinese
// (China)") to a short tag this package knows about. Returns "" for empty or
// unrecognized input so DetectLanguage can fall through to the next candidate.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	// Exact match on tag
	if _, ok := catalogs[s]; ok {
		return s
	}
	// Prefix match (e.g. "zh_CN.UTF-8" → "zh")
	for tag := range catalogs {
		if strings.HasPrefix(s, tag) {
			return tag
		}
	}
	// Alias match (e.g. "chinese" → "zh")
	for tag, aliases := range languageAliases {
		for _, a := range aliases {
			if strings.Contains(s, a) {
				return tag
			}
		}
	}
	if strings.HasPrefix(s, "en") || strings.Contains(s, "english") {
		return "en"
	}
	return ""
}
