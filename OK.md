# OK project memory

This file is loaded into every session's system prompt (the cache-stable prefix),
so keep it concise and durable — it is the project's standing instructions to the
agent.

## Conventions

- Go kernel under `internal/`; each package owns one concern and documents it in a
  package comment. Match the surrounding comment density and idiom when editing.
- One transport-agnostic `control.Controller` sits behind every frontend (chat
  TUI, HTTP/SSE serve, Wails desktop). Add behavior to the controller, not a
  frontend, so all three inherit it.
- Cache-first: the system-prompt prefix (base prompt + tools + memory) must stay
  byte-stable across turns so DeepSeek's automatic prefix cache stays warm. Never
  mutate it mid-session — ride the turn tail instead (see `control.Compose`).

## Memory

- Hierarchical docs: `OK.md` (this file, committed/shared), `OK.local.md`
  (personal, git-ignored), user-global `~/.config/ok/OK.md`, and any
  `OK.md` in an ancestor dir. Also accepted for backward compatibility.
- `@path` on its own line imports another file's contents.
- `#<note>` in chat quick-adds a line here. The `remember` tool saves durable
  facts to the per-project auto-memory store (frontmatter files + `MEMORY.md`
  index), which loads into the prefix on the next session.

## Notes
- Wails 构建需要在 Windows 上安装 Go 工具链。如在 PowerShell 中运行 `go build ./...`，确保 go 在 PATH 中。
- `seatbelt_windows.go` 中 `strings` import 已确认被使用（lines 113, 180, 192），之前的构建错误可能是旧版本问题。
- Building target: windows/amd64 — 确保构建前没有旧 ok.exe 在运行，否则 Wails 会报 "process cannot access the file" 错误。
- OK.md 保持精简：每轮都加载进系统提示词。勿用 #note 快速添加长文本。用 remember 工具存持久记忆。

