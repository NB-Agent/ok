<p align="center">
  <img src="docs/logo.svg" alt="OK" width="640"/>
</p>

<p align="center">
  <strong>English</strong>
  &nbsp;·&nbsp;
  <a href="./README.zh-CN.md">简体中文</a>
  &nbsp;·&nbsp;
  <a href="./docs/SPEC.md">Spec</a>
  &nbsp;·&nbsp;
  <a href="./CHANGELOG.md">Changelog</a>
</p>

> **The AI agent that writes its own playbooks.**  
> Self-evolving skills · ECP knowledge federation · ProofChain audit trail  
> OS-level sandbox · Multi-agent DAG orchestration · One engine, multiple frontends  
> **[Changelog →](CHANGELOG.md)**

<br/> | <br/>

<h3 align="center">Not a chatbot. An infrastructure for agents that grow.</h3>
<p align="center">A single 15 MB static binary. TUI, desktop, VS Code, JetBrains — one kernel, every surface. 7 chat platform bots in development. Sandbox-first, cache-stable, and wired to evolve itself over time.</p>

<br/>

> [!IMPORTANT]
> **Community · 加入社区** — bilingual Discord for setup help (`#help` / `#求助`), workflow showcases, and feature ideas. → **<https://discord.gg/XF78rEME2D>**

<br/>

## Why OK

Most coding agents are a conversation in a text box — powerful, but alone.
OK is **agent infrastructure**: it doesn't just answer your prompts, it builds
the pipes, protocols, and memory that let agents grow smarter together.

### 🧬 Self-Evolution — It Learns. It Writes. It Ships.

Every conversation teaches OK something. Every 3 turns it detects patterns
in your workflow. Every 6 turns it generates and validates skill candidates.
Every 10 turns it prunes what's no longer useful. Over time, OK writes its
own playbooks — without you ever configuring a thing.

### 🌐 ECP — Your Agents Shouldn't Work Alone

The Evolution Control Protocol lets multiple OK instances share learned skills
across machines. Your work laptop discovers patterns; your home machine
benefits. HMAC-authenticated, privacy-preserving, fully automatic. No other
agent has this.

### 🔗 ProofChain — Don't Trust. Verify.

Every tool execution is recorded in a SHA-256 hash chain. When OK says it
ran `go build` and it passed, that claim is cryptographically provable.
Not "trust me" — "verify me."

### 🧱 Civilization Primitives — The Social Contract for Agent Swarms

Under the hood, OK runs four standardized protocols that every agent instance
shares: **identity** (who am I), **recall** (what do I remember), **trust**
(what can I prove), and **learn** (what patterns have I found). These aren't
features — they're the social contract for a world where agents talk to agents.

### 🧭 DAG Reasoner — Think in Trees, Not Lines

Complex tasks are automatically decomposed into dependency-ordered plans.
Failed subtasks trigger re-decomposition with a different approach — not
a blind retry. When data is messy (OCR, scraped text), a data-probe phase
decides whether regex, fuzzy matching, heuristics, or AI extraction is the
right tool.

### 🏠 Everywhere You Work — One Engine, Infinite Frontends

One engine. Infinite frontends.

| Frontend | Status |
|----------|--------|
| Terminal TUI (bubbletea) | ✅ Production-ready |
| HTTP/SSE server (`ok serve`) | ✅ Production-ready |
| Wails desktop app | ✅ Production-ready |
| VS Code extension | ✅ CodeLens + InlineCompletion |
| JetBrains extension | ✅ JCEF WebView chat |
| Discord bot | 🟢 Implemented |
| Slack bot | 🟢 Implemented |
| Telegram bot | 🟢 Implemented |
| WeChat (企业微信) bot | 🟢 Implemented |
| DingTalk bot | 🟢 Implemented |
| Feishu bot | 🟢 Implemented |
| WhatsApp bot | 🟢 Implemented |
| Python SDK | 🟢 Implemented

### 🔒 Sandbox-First — Ask Permission, Not Forgiveness

Three-level permission system (`deny > ask > allow`) with per-tool glob
matching. OS-level bash jail: Windows AppContainer, Linux Landlock + seccomp-bpf,
macOS Seatbelt. Network egress controllable per session.

## Features (full list)

- **Three-level permission system.** `deny > ask > allow` with per-tool glob
  matching (`bash(rm -rf*)`, `read_file(.env)`). Per-session grants, team-enforced
  policy files (`~/.config/ok/policy.toml`), and `/permissions` live editing.
- **OS-level sandbox.** macOS Seatbelt, Linux Landlock + seccomp-bpf, Windows
  AppContainer (Win8+) or Low Integrity Level + directory ACL. Commands run
  confined — workspace writes only, network when allowed.
- **Custom sub-agents.** Define reusable agents in `.ok/agents/*.md` with
  YAML frontmatter: model override, tool whitelist, independent permission mode.
  Build multi-model Agent Teams with delegate tool.
- **Agent Store.** `ok agent list` / `install` / `publish` — community registry
  of shareable sub-agent definitions.
- **Deterministic audit trail.** Every tool execution logged to a SHA-256 hash
  chain. `ok audit` to view, `--json` for CI. Verifiable, non-repudiable.
- **CI/CD native.** 15 MB static binary, `ok ci --format json`, GitHub Action,
  Homebrew, npm, winget. Run agent tasks in your pipeline.
- **Code knowledge graph.** tree-sitter-powered symbol index (functions, structs,
  interfaces) injected into the system prompt. `go build -tags=treesitter` enables.
- **Multi-model & composable.** DeepSeek (flash/pro), Claude, OpenAI, local
  models. Run two models together (planner + executor) in separate, cache-stable
  sessions. Or build a team of specialists with different models.
- **ProofChain DST.** Deterministic compile/test verification per step. Every
  atom of work is proven before the next starts.
- **Plugin-driven.** MCP-compatible: stdio, Streamable HTTP, SSE. Hot-add
  servers mid-session with `/mcp add`.
- **Three frontends.** Terminal TUI (bubbletea), Wails Desktop, VS Code extension.
  One engine (`control.Controller`) behind all three.
- **Zero-friction distribution.** `CGO_ENABLED=0` single binary; cross-compile
  to six targets with one command. `ok setup` wizard gets you running in 30 seconds.

## Install / Build

```sh
make build      # -> bin/OK
make cross      # -> dist/ (darwin|linux|windows × amd64|arm64)
```

## Quick start

```sh
OK setup                      # config wizard → ./OK.toml
export DEEPSEEK_API_KEY=sk-...  # or put it in .env (see .env.example)
OK chat                       # then run /init to generate AGENTS.md (project memory)
OK run "implement the TODOs in main.go"
OK run --model mimo-pro "add unit tests for this function"
echo "explain this code" | OK run
```

## Configuration

Resolution order: **flag > `./OK.toml` > `~/.config/OK/config.toml` >
built-in defaults**. Secrets come from the environment via `api_key_env` and are
never stored in config files.

```toml
default_model = "deepseek-flash"   # executor; set [agent].planner_model to add a planner
# language    = "zh"               # ui language; empty = auto-detect from $LANG / $OK_LANG

[[providers]]
name        = "deepseek-flash"
kind        = "openai"
base_url    = "https://api.deepseek.com"
model       = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
# also preset: deepseek-pro, mimo-pro (mimo-v2.5-pro), mimo-flash (mimo-v2-flash) @ api.xiaomimimo.com/v1

[tools]
enabled = []   # omit/empty = all built-ins

[mode]
default = "normal"                               # plan | normal (default) | yolo
deny  = ["bash(rm -rf*)", "bash(git push*)"]     # blocked in every mode

[permissions]
# Deprecated: use [mode] instead. If both exist, [mode] wins.

[sandbox]
# workspace_root = ""          # file-writers confined here; empty = current dir
# allow_write    = ["/tmp"]    # extra dirs write_file/edit_file/multi_edit may touch

[[plugins]]
name    = "example"
command = "OK-plugin-example"
```

Mode selects the interaction style: `plan` (read-only, writers blocked),
`normal` (prompt before writers), or `yolo` (writers allowed without prompting).
`deny` rules block specific commands/tools in every mode (e.g. `bash(rm -rf*)`).
`OK chat` uses `normal` by default and prompts before writers (`y` once,
`a` this session, `n` no). `OK run` stays autonomous (headless) but still
honors `deny`. See [`docs/SPEC.md`](docs/SPEC.md) for the full schema.

Mode is the *policy* (how much to prompt). The **sandbox** is
*enforcement*: the file-writers (`write_file` / `edit_file` / `multi_edit`)
refuse any path outside `[sandbox] workspace_root` (default: the current dir, so
edits stay in the project), resolving symlinks and `..` so a link can't tunnel
out. Reads are unrestricted. `bash` is itself jailed on macOS by default
(`[sandbox] bash`, Seatbelt): commands may write only those same roots (plus
temp and toolchain caches) and reach the network only when `[sandbox] network`
is set. Other platforms fall back to running unconfined for now (see
`docs/SPEC.md` §9 for the escape-prompt and Linux support still to come).

### Plugins (MCP)

OK is an MCP client. A `[[plugins]]` entry's `type` selects the transport:
`stdio` (default) launches a local subprocess (`command`/`args`/`env`); `http`
(Streamable HTTP) connects to a remote `url` with optional static `headers`
(`${VAR}` / `${VAR:-default}` expanded from the environment, so tokens stay out
of the file). Tools surface to the model as `mcp__<server>__<tool>`; a tool
declaring MCP's `readOnlyHint: true` joins parallel dispatch and the permission
reader-default.

A server's **prompts** surface as `/mcp__<server>__<prompt>` slash commands
(positional args after the command); its **resources** are pulled in by writing
`@<server>:<uri>` in a message; `/mcp` lists connected servers and what each
exposes. `make build` also produces `bin/OK-plugin-example` — a runnable
reference stdio server (`echo`, `wordcount`, a `review` prompt, a style-guide
resource) you can copy.

```toml
[[plugins]]                       # local stdio server
name    = "example"
command = "OK-plugin-example"

[[plugins]]                       # remote server over Streamable HTTP
name    = "stripe"
type    = "http"
url     = "https://mcp.stripe.com"
headers = { Authorization = "Bearer ${STRIPE_KEY}" }
```

**Already have an `.mcp.json`?** Drop it in the project root and OK
reads it as-is — the `mcpServers` spec (`command`/`args`/`env`, `type`/`url`/
`headers`, `${VAR}` expansion) maps field-for-field onto `[[plugins]]`. Both
sources are merged; on a name collision `OK.toml` wins.

```json
{
  "mcpServers": {
    "filesystem": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path"] },
    "stripe": { "type": "http", "url": "https://mcp.stripe.com", "headers": { "Authorization": "Bearer ${STRIPE_KEY}" } }
  }
}
```

### Slash commands

In `OK chat`, built-in commands (`/compact`, `/new`, `/todo`, `/mcp`,
`/memory`, `/help`) run locally. **Custom commands** are Markdown files under
`.OK/commands/` (project) or `~/.config/OK/commands/` (user) —
`review.md` becomes `/review`, a subdirectory namespaces it (`git/commit.md` →
`/git:commit`). The body is a prompt template; invoking the command sends it as a
turn.

```markdown
---
description: Review the staged diff
argument-hint: [focus-area]
---
Review the staged diff. Focus on $ARGUMENTS, list bugs with file:line.
```

`$ARGUMENTS` expands to all space-separated args, `$1`…`$N` to positional ones.
MCP prompts also appear here as `/mcp__<server>__<prompt>`.

### @ references

Embed `@` references in a message and OK resolves them before sending, as
tagged context blocks: `@path/to/file` (or `@dir`) injects a local file's
contents (or a directory listing), and `@<server>:<uri>` injects an MCP
resource. A local path is only treated as a reference when it actually exists,
so ordinary `@mentions` stay literal. Typing `/` or `@` opens an autocomplete
menu — slash commands, or hierarchical file navigation (one directory level at a
time, descend into folders) plus MCP resources.

### Two-model collaboration (optional)

`OK setup` keeps first-run minimal: pick provider → keys (every SKU of a
chosen provider is enabled). Running two models together (executor + planner,
separate cache-stable sessions) is a one-line edit afterwards — set
`planner_model` to any other enabled provider:

```toml
[agent]
planner_model = "deepseek-pro"   # used as the low-frequency planner
```

## Architecture

Three tiers of extensibility, all behind registries the core resolves by name:

1. **Registry** — `Provider` and `Tool` are interfaces; the core has no `switch model`.
2. **Compile-time built-ins** — providers and tools self-register via `init()`; `main` blank-imports them.
3. **Runtime plugins** — MCP-compatible: stdio, Streamable HTTP, SSE. Hot-add servers mid-session.

See [`docs/SPEC.md`](docs/SPEC.md) for the full specification.

<br/>

## Acknowledgments

OK was born from **[Reasonix](https://github.com/esengine/DeepSeek-Reasonix)**,
an open-source AI agent framework. We're deeply grateful to the Reasonix team
for building the foundation — their architecture, rigor, and vision made OK possible.
This project carries their MIT license forward under Apache-2.0. See [NOTICE](NOTICE).

<br/>

---

<p align="center">
  <sub>Apache-2.0 — see <a href="./LICENSE">LICENSE</a></sub>
  <br/>
  <sub><a href="https://github.com/NB-Agent/ok">github.com/NB-Agent/ok</a> · <a href="https://nbyyds.com">nbyyds.com</a></sub>
</p>
