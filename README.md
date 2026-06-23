<p align="center">
  <img src="docs/logo.svg" alt="OK" width="400"/>
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

<p align="center">
  <a href="https://discord.gg/XF78rEME2D"><img src="https://img.shields.io/badge/discord-join-5865F2.svg?style=flat-square&logo=discord&logoColor=white" alt="Discord"/></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-8b949e.svg?style=flat-square" alt="license"/></a>
</p>

<br/>

> **Complex tasks. Infinite connections. Civilization infrastructure.**
>
> OK is not just another coding agent. It's a **cognitive kernel** that
> decomposes hard problems into verifiable steps, remembers everything it
> learns, and federates that knowledge across every instance it connects
> to. One 15 MB static binary. Zero config. No ceiling.

<br/>

## What OK is built for

Most coding agents are prompt wrappers — type a task, get a response, repeat.
OK is a **reasoning runtime**: it decomposes, executes, verifies, learns,
and connects.

```
                    ┌──────────────────┐
                    │   OK KERNEL      │
                    │                  │
                    │  identity        │
                    │  recall          │
                    │  trust           │
                    │  learn           │
                    └──────┬───────────┘
                           │
        ┌──────────────────┼──────────────────┐
        │                  │                  │
        ▼                  ▼                  ▼
   ┌─────────┐      ┌──────────┐      ┌──────────┐
   │ VERIFY  │      │  EVOLVE  │      │ CONNECT  │
   │         │      │          │      │          │
   │ DAG     │      │ Pattern  │      │ MCP      │
   │ YES/NO  │      │ → Skill  │      │ ECP      │
   │ Back-   │      │ → Share  │      │ Bridge   │
   │ track   │      │ → Grow   │      │ IM bots  │
   └─────────┘      └──────────┘      └──────────┘
```

Three loops that compound:

| Loop | What it does | Why it matters |
|---|---|---|
| **Verify** | DAG decomposition → YES/NO gate tree → method-ladder backtrack | Complexity doesn't collapse into prompt roulette |
| **Evolve** | Pattern detection → skill generation → validation → installation | Every session makes the next one cheaper |
| **Connect** | MCP plugins · ECP federation · P2P bridge · 7 IM platforms · VS Code · JetBrains | One kernel, every surface |

---

## VERIFY — the DAG Reasoner

The gap between LLMs and reliable outcomes is **verification**. OK closes it.

```
Goal: "Fix all goroutine leaks in this project"
        │
        ▼   LLM decomposes into DAG of YES/NO leaves
        │
   ┌────┴────┬────────┬────────┐
   │         │        │        │
   grep      read     read     read
   "go func" handler  worker   main
   │         │        │        │
   ✅ YES    ✅ YES   ❌ NO    ✅ YES
                       │
                 grep found nothing
                       │
              MethodLadder: regex → fuzzy
              "Don't grep — scan every file
               for goroutine-spawn patterns"
                       │
                 ✅ YES on retry
        │
   VerificationTree: root = AND(all leaves) = YES
```

Every node is a **Boolean proposition** with `file:line` evidence.
Failure doesn't re-ask the model — it escalates the **method**: regex → fuzzy → heuristic → AI.
Three methods fail → entire DAG re-decomposed with accumulated context.
→ [Reasoner spec →](docs/SPEC.md)

---

## EVOLVE — knowledge that compounds

OK is the only agent that **writes its own playbooks** from your usage.

```
Session 1: bash → grep → read_file → edit_file → go vet
Session 2: bash → grep → read_file → edit_file → go vet
Session 3: bash → grep → read_file → edit_file → go vet
                │
           pattern detected (3/6/10-turn cadence)
                │
           candidate generated: "search-and-fix.md"
                │
           validated against recent sessions
                │
           ✅ installed as reusable skill
                │
Session 10: "fix the race condition in handler"
            → skill auto-invoked
            → 3 steps instead of 10
```

Skills are plain Markdown in `.ok/skills/`. Edit them. Share them.
**ECP** (Evolution Control Protocol) federates them across machines with
HMAC authentication — what your work laptop learns, your home desktop
inherits. **Knowledge compounds across every OK instance you run.**
→ [Skills docs →](docs/SKILLS.md)

---

## CONNECT — one kernel, every surface

The OK kernel exposes 13 primitives (4 platform services + 5 LLM syscalls +
4 civilization primitives: **identity, recall, trust, learn**). Everything
outside the kernel is a plugin. The kernel has no `switch model`.

**Places OK lives today:**

- Terminal TUI · Wails Desktop · HTTP/SSE serve · VS Code · JetBrains
- Discord · Slack · Telegram · Feishu · DingTalk · WeChat · WhatsApp
- Any MCP client (OK exposes itself as an MCP server)

**What OK connects to:**

- 40+ built-in tools across 4 groups (core/advanced/knowledge/admin)
- 17 official MCP plugins (git, database, browser, OCR, voice, translate,
  search, deploy, workflow, and more)
- Hot-add any MCP server mid-session via stdio, HTTP, or SSE
- P2P bridge — OK instances discover each other and share tasks

**Security is not bolted on — it's the foundation:**

```
Layer 1 — Covenant (compile-time, immutable)
  "Never generate code that harms, deceives, or subverts."

Layer 2 — Permissions (runtime)
  deny > ask > allow · per-tool glob matching · team policy files

Layer 3 — OS sandbox (kernel-level)
  Windows  → AppContainer + Low Integrity + ACL + JobObject
  Linux    → Landlock + seccomp-bpf + network namespace
  macOS    → Seatbelt (sandbox-exec)

Layer 4 — Audit (cryptographic)
  SHA-256 hash chain · tamper-evident · ok audit
```

→ [Spec →](docs/SPEC.md) &nbsp;·&nbsp; [Security →](SECURITY.md)

---

## 30 seconds

```bash
export DEEPSEEK_API_KEY=sk-...     # or OPENAI_API_KEY, ANTHROPIC_API_KEY
ok chat                            # interactive TUI
ok run "add unit tests for handler.go"
ok run --model claude-sonnet "review the auth module"
```

```bash
git clone https://github.com/NB-Agent/ok.git && cd ok
make build          # → bin/ok (Go 1.25+)
make cross          # → dist/ (6 targets)
```

Also: Homebrew · npm · Winget · Docker.

---

## Where we are

| Area | Status |
|---|---|
| Kernel + Agent + DAG Reasoner + 40+ tools | ✅ Production |
| Self-evolution + ECP federation | ✅ Production |
| OS sandbox (3 platforms) | ✅ Production |
| MCP host + 17 official plugins | ✅ Production |
| TUI · Desktop · HTTP/SSE serve | ✅ Production |
| VS Code · JetBrains | ✅ Beta |
| IM bots (7 platforms) | ⚠️ Alpha — code complete, pending integration tests |
| P2P bridge (mDNS discovery) | 🔧 Planned |

→ [Changelog →](CHANGELOG.md)

---

## Community

- **[Discord](https://discord.gg/XF78rEME2D)** — bilingual, `#help` / `#求助`
- **[GitHub Issues](https://github.com/NB-Agent/ok/issues)**
- **[GitHub Discussions](https://github.com/NB-Agent/ok/discussions)**

---

OK was born from **[Reasonix](https://github.com/esengine/DeepSeek-Reasonix)**.
Their architecture laid the foundation. OK extends it with a verification
engine, self-evolving skills, ECP federation, and a formal kernel/civilization
model — building toward cognitive infrastructure anyone can run, connect,
and grow.

---
<p align="center">
  <sub>Apache-2.0 · <a href="./LICENSE">LICENSE</a> · <a href="https://github.com/NB-Agent/ok">github.com/NB-Agent/ok</a></sub>
</p>
