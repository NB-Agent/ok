# Changelog

All notable changes to OK are recorded here.

## [1.0.2] — 2026-06

### Added

- **DAG verification engine** (`internal/reasoner/`): deterministic YES/NO verdict
  parsing from sub-agent output, file:line evidence extraction with deduplication,
  AND/OR gate tree computation with post-order propagation, and an auto-escalating
  MethodLadder (regex → fuzzy → heuristic → AI). Integrated into Reasoner's
  re-decompose loop — previously the method ladder existed only as LLM prompt text.
- **`preview_edit` tool**: dry-run an edit against the current file content,
  returning the unified diff without writing to disk. ReadOnly tool for the LLM.
- **`edit_file` now supports `replace_all`**: previously only `multi_edit` had this
  parameter; now single edits can replace all occurrences.
- **Fuzzy match fallback**: when an exact `old_string` match fails, the edit engine
  retries with whitespace-tolerant regex matching (Aider-style). Catches tab/space
  drift and indentation mismatches.
- **Agent message bus** (`SetMsgBus`): publish tool execution events for external
  consumers.
- **Evidence ledger** in Agent: per-turn host-observed tool receipts.
- **Prefix shape tracking**: cache-hit diagnostics via `capturePrefixShape`.

### Changed

- **Plugin code simplified**: removed boilerplate across all 27 plugin files
  (net -824 lines). More consistent `plugin.json` manifest generation.
- **Dependencies updated**: Bubble Tea v2.0.7, Lipgloss v2.0.4, x/sys v0.46.0,
  x/term v0.44.0, x/text v0.38.0. Added chroma/v2 and go-keyring.
- **KeepPolicy bitmask**: replaced boolean compaction flags with a composable
  bitmask (`KeepErrors | KeepUserMarked`).

### Fixed

- **System prompt accuracy**: `precheckGoFile` description corrected from "runs go vet"
  to "checks Go syntax" — it uses `go/parser.ParseFile`, not `go vet`. Real semantic
  checks are post-write via DST.
- **`.gitignore`**: added patterns for root-level and plugin `*.exe` build artifacts.

## [1.0.1] — 2026-06

### Fixed

- **Desktop build failure**: `desktop/go.mod` declared `module ok/desktop` as a
  separate module, violating Go's `internal` package visibility rule when importing
  `github.com/NB-Agent/ok/internal/*`. Changed to `module github.com/NB-Agent/ok/desktop`
  so the desktop module sits under the root module tree and can legally access
  `internal/` packages.

## [1.0.0] — Initial Public Release — 2026-06

First open-source release under Apache-2.0. One static binary. Zero config
to start. Cross-instance knowledge federation (ECP), ProofChain audit,
self-evolving skill engine, multi-agent DAG orchestration with re-decomposition
backtrack, OS-level sandboxing, MCP plugin ecosystem.

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
- **Agent Teams**: multi-model team — orchestrator + specialists with independent
  sessions.
- **Custom sub-agent profiles**: `.ok/agents/*.md` with YAML frontmatter —
  model, tools, permission_mode, custom system prompt.
- **Agent Store**: `ok agent list/install/publish` — community registry.

### Security
- **OS-level sandbox**: Windows (AppContainer + LowIL + ACL + JobObject),
  Linux (Landlock + seccomp-bpf + netns), macOS (Seatbelt).
- **Three-level permission system**: deny > ask > allow with per-tool glob
  matching. Team policy file (`~/.config/ok/policy.toml`).
- **Audit trail**: SHA-256 hash chain. `ok audit` to view, JSON export.
- **Compound command matching**: `timeout nice npm install` correctly matches
  `allow = ["bash(npm install)"]`.

### Platforms & SDKs
- **Frontends**: TUI, Wails Desktop, HTTP/SSE, VS Code, JetBrains.
- **IM bots** (alpha): Discord, Slack, Telegram, Feishu, DingTalk, WeChat, WhatsApp.
- **Python SDK**: `AsyncAgent` + `AsyncAgentTransport` (async/await), Sphinx docs.
- **VS Code**: CodeLens (Explain/Fix), InlineCompletion (SSE-streamed), 6-language.
- **JetBrains**: JCEF WebView chat, JS bridge, `OkExplainIntention` lightbulb.

### Tools & Plugins
- 40+ built-in tools across core/advanced/knowledge/admin groups.
- **MCP plugin host**: stdio/HTTP/SSE transports, hot-add mid-session.
- 17 official MCP plugins: git, database, browser, OCR, voice, translate,
  search, deploy, workflow, and more.
- **`ok doctor`**: full environment diagnostics. **`ok ci`**: CI-optimized
  JSON output.

### Distribution
- Homebrew formula, npm package, Winget, Docker.
- `CGO_ENABLED=0` static binary, ~15 MB. Cross-compile for darwin/linux/windows × amd64/arm64.

## Pre-history (internal development)

v1 through v5 were internal milestones before the project was open-sourced.
This release consolidates all prior work into a single public release.
