---
key: episodic-12
type: episodic
created: 2026-06-21T11:44:44Z
source: auto-evolution
---

## Input
搞

## Output
---

## 完成

### 改了什么（12 个文件，13 行逻辑变更）

| 文件 | 改动 | 类型 |
|------|------|------|
| `internal/provider/openai/openai.go` | `tools` 字段移到 `messages` 之前 | 缓存 |
| `internal/provider/anthropic/anthropic.go` | `tools` 字段移到 `messages` 之前 | 缓存 |
| `internal/agent/task.go` | `AllNames()`→`Names()` | 子代理 |
| `internal/config/config.go` | 加简洁指令 | 输出 |
| `internal/tool/builtin/browser.go` | 255→99B | 描述 |
| `internal/tool/builtin/okverify_v2.go` | 238→93B | 描述 |
| `internal/tool/builtin/editfile.go` | 184→9...
