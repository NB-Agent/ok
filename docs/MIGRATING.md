# Migrating to OK 1.0 (the Go rewrite)

OK 1.0 is a **ground-up rewrite in Go**. It is a new codebase, not an
incremental upgrade of the `0.x` TypeScript releases. This guide explains what
changed and how to move over.

## TL;DR

| | Legacy (v1) | OK 1.0+ (v2) |
|---|---|---|
| Language | TypeScript / Node | Go |
| Branch | [`v1`](https://github.com/esengine/ok/tree/v1) (maintenance only) | `main` (default, active) |
| Versions | `0.x` (up to v0.54.x) | `1.0.0`+ |
| Install | `npm i -g ok` (npm ships the TS build) | `npm i -g ok` too — the package wraps the Go binary; or a release archive / `go build` |
| Code intelligence | embedding semantic search | bundled [CodeGraph](https://github.com/colbymchenry/codegraph) (symbol/call graph) |

"v1" and "v2" are **codebase generations**, not semver: the v1 line never reached
1.0, so the Go rewrite takes the `1.x` major.

## Installing 1.0

`npm` stays the primary channel — the package wraps the prebuilt Go binary (the
same way esbuild/biome ship native binaries via npm). The binary itself is a
standalone Go executable; npm is only the installer, not a runtime dependency.

```sh
npm i -g ok      # 1.0.0+ delivers the Go binary; 0.x is the legacy TS build
ok chat
```

Prebuilt archives (`ok-<os>-<arch>.tar.gz` / `.zip`) are also attached to
each GitHub release — on macOS/Linux they bundle the CodeGraph runtime beside the
binary, so code-intelligence works out of the box. Or build from source:

```sh
git clone https://github.com/esengine/ok   # default: main (Go)
cd ok && make build                        # -> bin/ok
```

Until `1.0.0` is published to npm, `npm i -g ok` still installs the `0.x`
TypeScript build — build from source (above) for the Go version meanwhile.

## Configuration

| Legacy | OK 1.0 |
|---|---|
| TS config files | `ok.toml` (project) / `~/.config/ok/config.toml` (user) — see `ok.example.toml` |
| env / API keys | `.env` or the environment (`DEEPSEEK_API_KEY`, `MIMO_API_KEY`, …) via `api_key_env` |
| project memory | `OK.md` (+ auto-memory), Claude-Code-compatible |
| MCP servers | `[[plugins]]` in `ok.toml`, or a Claude-Code `.mcp.json` (read as-is) |

## What's the same

The agent core carries over: the loop, tools (read/write/edit/glob/grep/bash/…),
subagents (`task`, explore/research/review), skills, hooks, plan mode, MCP client,
and DeepSeek prefix-cache–oriented design.

## What's different

- **Code intelligence**: embedding semantic search is replaced by **CodeGraph**
  (`codegraph_*` tools) — a tree-sitter symbol/call graph, no embedding service or
  API cost. Shipped built-in.
- **Plan mode** + `complete_step` (evidence-backed step sign-off).
- **No web dashboard** — the v2 line is terminal + desktop (Wails), by design.
- Some granular v1 tools are intentionally consolidated (e.g. file-management ops
  go through `bash`); a few v1 tools are not yet ported (tracked on Discussions).

## Reporting issues

Issues and PRs are labeled by line: **`v1`** (legacy TypeScript) and **`v2`**
(Go). File new reports against the line you're using. The legacy `v1` line is in
maintenance mode — bug fixes only, no new features.

Questions? Open a [Discussion](https://github.com/esengine/ok/discussions).
