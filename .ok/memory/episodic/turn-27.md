---
key: episodic-27
type: episodic
created: 2026-06-21T09:19:03Z
source: auto-evolution
---

## Input
但是每次都这样也不行啊

## Output
---

## ✅ 修好了。改了什么：

| 文件 | 改动 | 效果 |
|------|------|------|
| `internal/boot/boot.go:842-851` | 共享知识索引截断到 **30 条** | 前缀再也不会因 207 条共享记忆膨胀 |
| `internal/memory/store.go:28-32` | Store 结构体加 `maxEntries` 字段 | 每个 store 可独立设上限 |
| `internal/memory/store.go:139-142` | prune 用 `s.maxEntries` 而非硬编码常量 | 项目记忆和共享记忆各用各的帽 |
| `internal/memory/shared.go:8-10,21` | `MaxSharedMemoryEntries = 100` | 共享记忆文件 >100 条自动淘汰最旧的 |

---

## 📊 效果

```
之前: 共享记忆 207 条全注入前缀 → ~6,000 tokens
现在: 最多注入 30 条              → ~900...
