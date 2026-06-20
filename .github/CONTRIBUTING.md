# Contributing to OK

Thanks for your interest in contributing! OK is a config-driven AI coding agent for the terminal, built in Go.

## Getting Started

```bash
git clone https://github.com/esengine/ok.git
cd ok

# Build
make build

# Run tests
make test

# Run linter (requires golangci-lint)
make lint
```

### Prerequisites

- Go (see `go.mod` for minimum version)
- `golangci-lint` (for linting)
- `make` (optional; you can run `go` commands directly)

## Project Layout

```
ok/
├── cmd/
│   ├── ok/main.go              # CLI entry point
│   └── ok-plugin-example/      # Reference MCP plugin
├── internal/
│   ├── agent/         # Core agent harness (run loop, task decomposition)
│   ├── boot/          # System prompt assembly (prefix-stable for cache)
│   ├── cli/           # Terminal TUI (Bubble Tea) and subcommands
│   ├── config/        # TOML config loading (flag > project > user > defaults)
│   ├── control/       # Transport-agnostic Controller (shared by all frontends)
│   ├── core/          # Core types (message atoms, proof chain)
│   ├── hook/          # Pre/post tool hooks (settings.json)
│   ├── memory/        # Project memory (OK.md, remember tool)
│   ├── permission/    # Per-call allow/ask/deny policy
│   ├── plugin/        # MCP client (stdio + Streamable HTTP transports)
│   ├── provider/      # LLM provider interface + registry
│   │   └── openai/    # OpenAI-compatible /chat/completions
│   ├── sandbox/       # File writer confinement + Landlock/Seatbelt
│   ├── serve/         # HTTP/SSE server
│   ├── skill/         # Skill system (built-in + installable playbooks)
│   └── tool/          # Tool interface + registry
│       └── builtin/   # bash, read_file, write_file, edit_file, glob, grep, etc.
└── desktop/           # Wails desktop app (React + TypeScript)
```

## Architecture Principles

1. **Interface-first, registry-based** — `Provider` and `Tool` are Go interfaces; implementations register via `init()`
2. **Transport-agnostic** — `control.Controller` is the single business-logic entry behind TUI, HTTP/SSE, ACP, and desktop
3. **Cache-first** — system prompt prefix must stay byte-stable across turns for DeepSeek's prefix cache
4. **Two-tier extensibility** — compile-time built-ins (blank import) + runtime MCP plugins
5. **Single static binary** — `CGO_ENABLED=0`, no runtime dependencies

## Conventions

### Code Style

- **GoDoc on every export** — types, functions, constants, and package declarations
- **`gofmt` for formatting** (enforced in CI)
- **Standard `if err != nil`** error handling throughout
- **Use `fmt.Errorf("...: %w", err)`** to wrap errors (preserves `errors.Is`/`errors.As` chains)
- **Only `panic` for startup invariants** (duplicate registrations, init-time config errors); never in request-path code
- **Use `recover()` at tool/boundary edges only** (agent dispatch, sub-agent spawn, job execution)

### Testing

- Every new package must have at least one `_test.go` file
- Use table-driven tests for multi-case functions
- Add fuzz tests for parsing/untrusted-input functions
- Add benchmarks for performance-sensitive paths (stream processing, diff, compaction)
- CI runs `-race` on all three platforms

### Commit Messages

- Use present tense, imperative mood ("Add X" not "Added X")
- Reference issues where applicable
- Keep first line under 72 characters

## Adding a Provider

1. Create a new package under `internal/provider/<name>/`
2. Implement the `provider.Provider` interface
3. Register via `func init() { provider.Register("kind", New) }`
4. Blank-import in `cmd/ok/main.go`

## Adding a Built-in Tool

1. Implement the `tool.Tool` interface
2. Register via `func init() { tool.RegisterBuiltin(myTool) }`
3. The tool package's `init` auto-registers — new files are picked up automatically

## Questions?

Open a [discussion](https://github.com/esengine/ok/discussions) or join the community channel (TODO: link).
