# Architecture Decision Records

This directory records significant architectural decisions for OK.
Each ADR describes a decision, its context, alternatives considered, and consequences.

## Index

| # | Title | Status | Date |
|---|-------|--------|------|
| 1 | [Go rewrite from TypeScript](#1-go-rewrite-from-typescript) | Accepted | 2025-Q4 |
| 2 | [Interface-first registry pattern](#2-interface-first-registry-pattern) | Accepted | 2025-Q4 |
| 3 | [Transport-agnostic Controller](#3-transport-agnostic-controller) | Accepted | 2025-Q4 |
| 4 | [Cache-stable system prompt prefix](#4-cache-stable-system-prompt-prefix) | Accepted | 2025-Q4 |
| 5 | [MCP for runtime plugin extensibility](#5-mcp-for-runtime-plugin-extensibility) | Accepted | 2025-Q4 |
| 6 | [Landlock sandbox over Docker/VM isolation](#6-landlock-sandbox-over-dockervm-isolation) | Accepted | 2025-Q4 |
| 7 | [Single static binary distribution](#7-single-static-binary-distribution) | Accepted | 2025-Q4 |
| 8 | [Bubble Tea TUI over web-based UI](#8-bubble-tea-tui-over-web-based-ui) | Accepted | 2025-Q4 |
| 9 | [Proof-chain verified task decomposition](#9-proof-chain-verified-task-decomposition) | Accepted | 2025-Q4 |

---

## 1. Go rewrite from TypeScript

**Status**: Accepted  
**Date**: 2025-Q4

### Context

The original OK 0.x was a TypeScript/Node.js CLI. As the project grew toward a multi-frontend agent with sandboxing, streaming, and desktop support, we hit several walls:

- Node.js startup latency (cold start ~200ms) was unacceptable for a CLI tool
- NPM dependency tree bloat made reproducible builds fragile
- Cross-platform distribution required Node.js runtime on the user's machine
- Memory footprint was high for long-running agent sessions
- Concurrent tool execution required careful async management

### Decision

Rewrite from the ground up in **Go**, targeting a single static binary.

### Alternatives Considered

1. **Stay with TypeScript + Bun** — Faster startup but still requires a runtime; native addons break on cross-compile.
2. **Rust** — Excellent fit for performance/safety, but slower development velocity and steeper learning curve for community contributors.
3. **Python** — Largest AI ecosystem but poor distribution story and high resource usage.

### Consequences

- ✅ Single binary: `CGO_ENABLED=0 go build`, zero runtime dependencies
- ✅ Sub-millisecond startup, low memory footprint
- ✅ First-class cross-compilation: 6 OS/arch targets from one command
- ✅ Goroutines for concurrent tool dispatch with bounded semaphores
- ❌ Lost access to the Node.js MCP ecosystem's npm packages (mitigated by writing our own MCP client)
- ❌ Took 3 months of sustained effort to reach feature parity

---

## 2. Interface-first registry pattern

**Status**: Accepted  
**Date**: 2025-Q4

### Context

OK needed to support multiple LLM providers (OpenAI, Anthropic, DeepSeek, local models) and multiple tool implementations (built-in + MCP plugins). Hardcoding provider or tool switching in the agent loop would create a maintenance nightmare.

### Decision

Define Go interfaces for `Provider` and `Tool`, with a `Register` + factory pattern. Packages self-register via `func init()`.

```go
// provider/provider.go
type Provider interface {
    Kind() string
    Chat(ctx context.Context, req *ChatRequest) (<-chan Chunk, error)
}

func Register(kind string, factory func(*Config) (Provider, error)) { ... }

// tool/tool.go
type Tool interface {
    Name() string
    Description() string
    Run(ctx context.Context, args string) (string, error)
}
```

### Alternatives Considered

1. **Map-based dispatch with switch statements** — Simple but violates Open/Closed Principle; every new provider requires touching core agent code.
2. **Plugin system via go-plugin over net/rpc** — Over-engineered for this use case; adds process-boundary overhead.
3. **YAML/JSON-driven reflection** — Fragile, hard to debug, and Go's reflection is not type-safe.

### Consequences

- ✅ Adding a provider = new package + `init()` + blank import in `main.go`
- ✅ Adding a tool = new file in `tool/builtin/` — auto-discovered
- ✅ Compile-time safety: no runtime type confusion
- ✅ Tests can easily mock `Provider` and `Tool` interfaces
- ❌ `init()` ordering is implicit — must ensure no inter-package init dependencies

---

## 3. Transport-agnostic Controller

**Status**: Accepted  
**Date**: 2025-Q4

### Context

OK needed three frontends at launch (CLI/TUI, HTTP/SSE for web embedding, and ACP for IDE integration) with a Wails desktop app on the roadmap. Duplicating agent orchestration logic across frontends would be the #1 source of bugs.

### Decision

Extract all business logic into `control.Controller`. Frontends consume a typed `event.Sink` stream.

```
┌──────────┐  ┌──────────────┐  ┌──────────┐
│ cli/TUI  │  │ serve/HTTP   │  │ acp/IDE  │
│ (Bubble  │  │ (SSE)        │  │ (JSON-   │
│  Tea)    │  │              │  │  RPC)    │
└────┬─────┘  └──────┬───────┘  └────┬─────┘
     │               │               │
     └───────────────┼───────────────┘
                     │
           ┌─────────┴──────────┐
           │ control.Controller │
           │ (single impl)      │
           └─────────┬──────────┘
                     │
           ┌─────────┴──────────┐
           │    agent.Agent     │
           └────────────────────┘
```

### Alternatives Considered

1. **Shared library functions** — Still allows frontend-specific logic to drift; doesn't enforce a single entry point.
2. **gRPC service** — Adds process overhead, breaks single-binary story, complicates debugging.

### Consequences

- ✅ All new features automatically available on all frontends
- ✅ Single concurrency guard (`runGuarded`) prevents double-execution
- ✅ Typed event stream enables rich rendering (tool calls, approvals, reasoning blocks)
- ❌ Event type must satisfy all frontend needs simultaneously — some are no-ops for certain frontends
- ❌ Adding a new event type requires changes to every frontend renderer

---

## 4. Cache-stable system prompt prefix

**Status**: Accepted  
**Date**: 2025-Q4

### Context

DeepSeek's API offers automatic prefix caching: if the prefix of consecutive requests is byte-identical, the KV-cache is reused and tokens are billed at 10% cost. The system prompt (base instructions + tool definitions + memory + skill index) is typically the largest and most static part of each request. Mutating it mid-session evicts the cache.

### Decision

Keep the system prompt prefix **byte-stable across turns** within a session. All dynamic content (language policy updates, memory additions, skill body injection) rides in the **transient turn tail** (user message prefix) which is intentionally excluded from the cached prefix.

Architecture:
- `boot.Build()` assembles the initial system prompt once
- Mid-session changes to memory/skills are queued and injected into the next user turn
- The system prompt itself is never mutated after `session.New`

### Alternatives Considered

1. **Rebuild prompt every turn** — Simplest but loses all cache benefit (5-10x token cost increase).
2. **Separate cache-stable + dynamic system messages** — DeepSeek's cache boundary is at the request level, not per-message.
3. **Manual `cache_control` breakpoints** — Anthropic-style explicit markers; DeepSeek doesn't support them.

### Consequences

- ✅ 5-10x token cost reduction for long sessions
- ✅ Memory/skills updates feel instant (queued in next user turn, not waiting for system prompt rebuild)
- ❌ Must be vigilant about any code path that touches the system prompt string
- ❌ Debugging cache misses requires byte-level diff comparison

---

## 5. MCP for runtime plugin extensibility

**Status**: Accepted  
**Date**: 2025-Q4

### Context

Users need to bring their own tools (database clients, internal APIs, custom integrations). Compile-time built-ins (`//go:generate` or code generation) are too restrictive for end users who don't want to compile from source.

### Decision

Implement an MCP (Model Context Protocol) client supporting two transports:
1. **stdio** (JSON-RPC 2.0 over NDJSON) — for local subprocess plugins
2. **Streamable HTTP + SSE** — for remote tool servers

Tools from MCP servers surface to the model as `mcp__<server>__<tool>` and are added to the runtime tool registry.

### Alternatives Considered

1. **Lua/Python scripting** — Full programmability but massive attack surface; sandboxing script engines is notoriously hard.
2. **WASM plugins** — Secure by default but the Go WASM runtime ecosystem is young and limited.
3. **gRPC service mesh** — Complex deployment, breaks the zero-config goal.

### Consequences

- ✅ Users can write plugins in any language (Python, Node.js, Rust, etc.)
- ✅ Clean process isolation — a crashing plugin cannot take down the agent
- ✅ Hot-add/remove at runtime via `/mcp add` / `/mcp remove`
- ✅ Reuses the industry-standard MCP protocol (interop with Claude Desktop, Continue, etc.)
- ❌ Subprocess management adds complexity (startup, health checks, graceful shutdown)
- ❌ JSON-RPC is less efficient than binary protocols for high-frequency tool calls

---

## 6. Landlock sandbox over Docker/VM isolation

**Status**: Accepted  
**Date**: 2025-Q4

### Context

The `bash` tool executes arbitrary user-requested shell commands, and `write_file`/`edit_file` modify the filesystem. Without confinement, a model hallucination could `rm -rf` the user's home directory or exfiltrate `.ssh` keys.

### Decision

Use kernel-level unprivileged sandboxing:
- **Linux**: Landlock (kernel 5.13+) — path-based allowlisting with no root required
- **macOS**: Seatbelt (sandbox-exec) — declarative policy language, built into Darwin

The sandbox policy:
- Read access: project root + system temp
- Write access: project root only (via confined file writer)
- Network: blocked (Landlock) / restricted (Seatbelt)
- No access to home directory, `/etc`, `/proc`, dotfile directories

### Alternatives Considered

1. **Docker containers** — Heavy (hundreds of MB), requires Docker daemon, slow startup, Linux-only.
2. **Firecracker microVMs** — Excellent isolation but requires KVM, not available on all machines.
3. **chroot / pivot_root** — Requires root; not suitable for a user CLI tool.
4. **No sandbox — rely on model alignment** — Too risky; even frontier models hallucinate dangerous commands.

### Consequences

- ✅ Zero-dependency sandbox (part of the kernel)
- ✅ No root required — works for any user
- ✅ Negligible performance overhead (path-based checks in VFS layer)
- ❌ macOS Seatbelt requires `sandbox-exec` which may be restricted on managed/corporate devices
- ❌ Windows has no equivalent — sandbox is no-op on Windows (documented limitation)
- ❌ Landlock requires kernel 5.13+ (Ubuntu 22.04+, not CentOS 7)

---

## 7. Single static binary distribution

**Status**: Accepted  
**Date**: 2025-Q4

### Context

AI coding tools are downloaded by developers across platforms. Requiring a runtime (Python, Node.js, JVM) or a package manager (npm, pip, brew) creates friction and excludes air-gapped environments.

### Decision

Distribute as a single statically-linked Go binary (`CGO_ENABLED=0`). The binary includes:
- The agent kernel
- The CLI/TUI frontend (Bubble Tea)
- The HTTP/SSE server
- Embedded `index.html` for the browser client
- Built-in tools and OpenAI provider

### Alternatives Considered

1. **Homebrew + apt + npm packages** — Broader discoverability but maintenance burden of 5+ package repos.
2. **AppImage / Flatpak / MSI** — Better desktop integration but complex build pipelines.
3. **Go install** — Requires Go toolchain on user's machine; not suitable for non-developers.

### Consequences

- ✅ `curl ... | tar xz && ./ok` — one-command install
- ✅ Air-gapped deployment: copy a single file
- ✅ Cross-compile to 6 targets from macOS/Linux CI: `make cross`
- ✅ Trivial CI: `go build` is the entire build step
- ❌ No auto-update mechanism (users must re-download)
- ❌ Binary size ~25MB (acceptable for a dev tool, but larger than a shell script)

---

## 8. Bubble Tea TUI over web-based UI

**Status**: Accepted  
**Date**: 2025-Q4

### Context

OK needed a rich interactive interface for plan mode (with approval UI), streaming markdown rendering, and permission prompts. The two obvious paths were a terminal TUI or a web-based UI served locally.

### Decision

Use **Charmbracelet Bubble Tea v2** (Elm Architecture for the terminal) with Lipgloss styling and Glamour/Goldmark for markdown rendering.

### Alternatives Considered

1. **React + localhost web app** — More familiar to frontend developers, richer styling, but requires a browser (not always available on remote/headless machines), feels out of place in a terminal tool.
2. **tview/tcell** — Mature but lower-level; more boilerplate for the streaming update model we need.
3. **Raw ANSI escape sequences** — Maximum control, nightmare to maintain.

### Consequences

- ✅ Terminal-native: works over SSH, in tmux, on headless machines
- ✅ Bubble Tea's Elm Architecture maps naturally to our event stream
- ✅ Lipgloss provides modern terminal styling (gradients, borders, padding)
- ❌ Terminal rendering quirks across different emulators (Windows Terminal vs iTerm2 vs Alacritty)
- ❌ Limited compared to web: no images, no syntax-highlighted code blocks (mitigated by Goldmark)

---

## 9. Proof-chain verified task decomposition

**Status**: Accepted  
**Date**: 2025-Q4

### Context

For multi-step agent tasks, the model decomposes a goal into sub-tasks, spawns sub-agents to verify each, and aggregates results. Without tamper-evident logging, a sub-agent could claim a false result, or reasoning chains could be fabricated post-hoc.

### Decision

Implement a **SHA-256 hash chain** (`core.ProofChain`) that links each verification step:
- Each atom (message, tool call, result) is hashed
- Each atom includes the hash of its predecessor
- The final chain hash cryptographically binds the entire reasoning trace

### Alternatives Considered

1. **Plain text logging** — No tamper resistance; suitable for debugging but not verification.
2. **Signed attestations (Ed25519)** — Stronger than hash chain but requires key management; overkill for an agent audit trail.
3. **Merkle tree** — Better for selective disclosure but adds complexity without clear benefit for our linear chain model.

### Consequences

- ✅ Tamper-evident: any modification to the reasoning chain is detectable
- ✅ Verifiable by third parties: share the chain hash + trace to prove what happened
- ✅ Negligible performance overhead (SHA-256 is hardware-accelerated on all platforms)
- ❌ Does not prevent fabrication — a malicious agent can still produce a valid chain of false claims
- ❌ Chain breaks require full recomputation (acceptable: rare operation, O(n))
