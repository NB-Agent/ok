# Contributing to OK

Thanks for your interest in contributing! OK is an AI coding agent built in Go with a plugin-driven architecture.

## Code of Conduct

All contributors are expected to follow our [Code of Conduct](CODE_OF_CONDUCT.md).

## Getting started

```sh
git clone https://github.com/NB-Agent/ok.git
cd ok
go build ./...        # verify everything compiles
go test ./...         # run all tests
```

You'll need Go 1.25+.

## Project structure

```
ok/
├── cmd/ok/               # main entrypoint
├── cmd/ok-*-bot/         # chat platform bots (Discord, Slack, etc.)
├── internal/
│   ├── agent/            # LLM agent loop, sub-agents, execution
│   ├── boot/             # composition root — wires everything together
│   ├── cli/              # command-line interface (TUI)
│   ├── control/          # transport-agnostic controller
│   ├── evolution/        # ECP: cross-instance knowledge federation
│   ├── kernel/           # civilization primitives (identity, recall, trust, learn)
│   ├── plugin/           # MCP plugin host
│   ├── provider/         # LLM providers (OpenAI-compatible + Anthropic native)
│   ├── sandbox/          # OS-level sandbox
│   ├── serve/            # HTTP/SSE server
│   ├── skill/            # skill playbook registry
│   └── tool/             # tool registry + built-in tools
├── plugins/              # bundled MCP plugins
├── prompts/              # system prompt templates
├── editors/              # IDE extensions (VS Code, JetBrains)
├── desktop/              # Wails desktop app
├── sdk/python/           # Python SDK
└── docs/                 # documentation
```

## Making changes

1. Create a branch from `master`.
2. Make your changes. Keep them focused — one concern per PR.
3. Ensure `go build ./...` and `go test ./...` pass.
4. Run `go vet ./...` to catch common issues.
5. Format with `gofmt` (or `go fmt ./...`).
6. Open a pull request with a clear description.

## Commit style

- Use present-tense, imperative mood: "Add X" not "Added X"
- Prefix with the package: `agent:`, `plugin:`, `docs:`
- Keep commits atomic — one logical change per commit

## Adding a new MCP plugin

1. Create `plugins/ok-<name>/main.go` implementing MCP JSON-RPC over stdin/stdout.
2. Add a `plugin.json` with name, version, tools, and entrypoint.
3. Tool names MUST be unique across all bundled plugins.

## Documentation

- Package comments (`// Package foo ...`) are required on every package.
- Exported symbols should have doc comments.
- Architecture decisions go in `docs/`.

## Questions?

Open a discussion or issue on GitHub.
