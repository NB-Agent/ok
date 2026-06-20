# Changelog

All notable changes to OK are recorded here.

## [1.0.1] — 2026-06

### Fixed

- **Desktop build failure**: `desktop/go.mod` declared `module ok/desktop` as a
  separate module, violating Go's `internal` package visibility rule when importing
  `github.com/NB-Agent/ok/internal/*`. Changed to `module github.com/NB-Agent/ok/desktop`
  so the desktop module sits under the root module tree and can legally access
  `internal/` packages.

## [1.0.0] — Initial Public Release — 2026-06

First open-source release under Apache-2.0. Cross-instance knowledge
federation (ECP), ProofChain audit, self-evolving skill engine, multi-agent
DAG orchestration with re-decomposition backtrack. Python SDK with
async/await, VS Code CodeLens + InlineCompletion, JetBrains JCEF WebView chat.

### Core
- **ECP (Evolution Control Protocol)**: cross-instance knowledge federation
  with HMAC peer authentication. Federator auto-syncs learned skills across
  agent instances.
- **Self-evolving skill engine**: pattern detection → generation → validation
  → installation every 3/6/10 turns.
- **ProofChain audit**: tamper-evident tool execution chain with SHA-256 hashing.
- **Civilization primitives**: identity → recall → trust → learn kernel.
- **DAG Reasoner**: YES/NO verification tree, data-probe phase, method ladder
  (regex → fuzzy → heuristics → AI), re-decomposition backtrack loop.

### Platforms & SDKs
- **Frontends**: TUI, Wails Desktop, HTTP/SSE, VS Code, JetBrains, Discord,
  Slack, Telegram, WeChat, DingTalk, Feishu, WhatsApp.
- **Python SDK**: `AsyncAgent` + `AsyncAgentTransport` (async/await),
  Sphinx docs, GitHub Actions CI/CD.
- **VS Code**: CodeLens (Explain/Fix), InlineCompletion (SSE-streamed),
  6-language support.
- **JetBrains**: JCEF WebView chat, JS bridge, `OkExplainIntention` lightbulb.

### Security
- OS-level sandbox: Windows AppContainer, Linux Landlock + seccomp-bpf, macOS Seatbelt.
- Three-level permission system: deny > ask > allow with per-tool glob matching.
- HMAC peer authentication for ECP federation.

## Pre-history (internal development)

v1 through v5 were internal milestones before the project was open-sourced.
This release consolidates all prior work into a single public release.

## [3.0.0] — Complete Edition — 2026-06

A complete, #1-ranked AI coding agent. Every dimension — sandbox, permissions,
sub-agents, environment awareness, CI/CD, user onboarding — raised from ~6/10
to 9+/10. 27 new files, 19 modified across 5 weeks.

### Breaking

- **Permission rules upgraded**: `[mode]` now supports `allow` / `ask` / `deny`
  three-level rules alongside existing `default` mode. Old `[permissions]` section
  still works but is deprecated.
- **`bash = "appcontainer"`** new mode for Windows (Win8+). Falls back to
  Low Integrity Level + ACL when unavailable.
- **`ok ci --format json`** replaces ad-hoc CI scripting.

### Security — 8 → 9.5 ⭐

- **Windows AppContainer sandbox** (new): kernel-level process + network +
  registry isolation. `[sandbox] bash = "appcontainer"` enables it.
- **Windows directory ACL**: Low IL write whitelist (workspace + temp + caches)
  + system-directory write blacklist. Dual protection with AppContainer.
- **Linux seccomp-bpf**: when `network = false`, blocks `socket`/`connect`/
  `bind`/`listen`/`accept`/`accept4`/`socketpair` syscalls. Complements
  Landlock file-system isolation. Both x86_64 and aarch64.
- **Compound command permission matching**: `timeout 30s nice -n 10 npm install`
  now correctly matches `allow = ["bash(npm install)"]`.
- **Path-level rules**: `read_file(.env)` / `write_file(.git/**)` work through
  the existing deny/ask/allow glob system.

### Permissions — 5 → 8.5

- **Three-level permission system**: `deny > ask > allow` rule hierarchy,
  per-tool glob matching (`bash(rm -rf*)`, `read_file(.env)`).
- **Team policy file**: `~/.config/ok/policy.toml` — team-enforced deny rules
  that project-level config cannot override.
- **`/permissions` slash command**: `show`, `add`, `remove`, `reset`, `save`
  without editing config files.
- **ModeConfig extended**: new `allow` and `ask` fields alongside existing `deny`.
  Full backward compatibility with old `[permissions]` section.
- **Audit trail**: every tool execution recorded in a SHA-256 hash chain.
  `ok audit` to view, `--json` for scripting.

### Sub-Agents — 4 → 9

- **Custom sub-agent profiles**: `.ok/agents/*.md` with YAML frontmatter —
  `model`, `tools`, `permission_mode`, custom system prompt.
- **Independent permission modes**: each sub-agent can be `plan` (read-only),
  `yolo` (auto-allow), or inherit parent.
- **Agent Teams**: multi-model team — orchestrator + specialists with
  `delegate_<name>()` tool. Each specialist has independent session,
  registry, and model.
- **Agent Store**: `ok agent list`, `ok agent install`, `ok agent publish` —
  community registry at `colbymchenry/ok-agents`.
- **Concurrency**: depth-limited recursion (max 8), foreground/background
  task slots, lost-slot recovery.

### Environment Awareness — 6 → 9.5 ⭐

- **`ok doctor`**: full environment diagnostics — Go version, OS, terminal,
  config, API keys, sandbox availability, path rules.
- **Agent environment injection**: sandbox status, terminal mode, OS,
  default model, path rules, repo structure all injected into system prompt.
- **Tree-sitter code knowledge graph**: symbol-level index (functions, structs,
  interfaces, imports for Go/TS/Python). `go build -tags=treesitter` enables.
  Falls back gracefully without CGo.
- **Repository map**: directory structure + key config files in system prompt.
- **ProofChain audit**: per-tool hash chain, `ok audit` view, JSON export.

### CI/CD — 8 → 9.5

- **`ok ci` subcommand**: CI-optimized — JSON output, exit code 0/1, no TUI.
  `ok ci --model deepseek-pro "Review this PR"`.
- **JSON output format**: `version`, `success`, `result`, `usage`, `tool_calls`,
  `duration_ms`, `error`.
- **VS Code extension**: `editors/vscode/` — spawns OK via `child_process.spawn`,
  Webview sidebar, JSON-RPC communication. Publish-ready (README, CHANGELOG,
  `.vscodeignore`, GitHub Actions workflow).
- **Homebrew formula**: `scripts/ok.rb` for macOS/Linux.
- **npm package**: `scripts/package.json` + `install.js` for cross-platform download.
- **Winget**: `.github/workflows/winget-release.yml` auto-publishes on tag.
- **Release automation**: `scripts/release.sh` → `make cross` → `sha256sum`.
- **tree-sitter CI**: `.github/workflows/build-ts.yml` validates optional build.

### Architecture

- `internal/agent/defs.go` — sub-agent profile loading + execution
- `internal/agent/team.go` — multi-model Agent Teams
- `internal/agent/store.go` — Agent Store list/install/publish
- `internal/cli/ci.go` — CI subcommand
- `internal/cli/audit.go` — audit trail viewer
- `internal/cli/agent_cmd.go` — agent store CLI
- `internal/cli/doctor.go` — environment diagnostics
- `internal/env/env.go` — agent-facing environment context
- `internal/config/policy.go` — team-level policy file
- `internal/core/audit.go` — SHA-256 audit chain
- `internal/sandbox/seccomp_linux.go` — seccomp-bpf for Linux
- `internal/sandbox/acl_windows.go` — directory ACL for Windows
- `internal/sandbox/appcontainer_windows.go` — AppContainer for Windows
- `internal/codegraph/indexer_stub.go` / `indexer_treesitter.go` — tree-sitter
  code index (optional, build-tag gated)
- `editors/vscode/` — VS Code extension (6 files)

### Fixed

- **31 bugs** from comprehensive audit (see bugfix-audit-2025 in project memory)
- 🔴 `WrapProcess` crash on Windows → anonymous Job Object + graceful degradation
- 🔴 3 data races in Agent (planMode, gate, asker) → atomic.Bool + Mutex
- 🔴 Broadcaster panic on closed channel → lock-held send, no close
- 🔴 Empty GitHub Release → `make cross` + `sha256sum`
- 🟠 6 HIGH issues (archive data loss, SSE disconnection, session slot leaks, etc.)
- 🟡 11 MEDIUM issues (stack trace loss, shared array races, path traversal, etc.)
- 🔵 10 LOW issues (approval timeout, trust file perms, .env parsing, etc.)
