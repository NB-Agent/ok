---
key: episodic-6
type: episodic
created: 2026-06-21T12:48:11Z
source: auto-evolution
---

## Input
现在发行是1.0.2了

## Output
6 个 CLI 二进制都构建成功了！现在唯独**桌面端** `ok-windows-desktop.exe` 需要确认——桌面端依赖 Wails + 前端（pnpm build + wails build），比较重。要一起重新构建桌面端吗？

当前状态：

| 二进制 | 状态 |
|--------|------|
| ✅ `release/ok-windows-amd64.exe` | 已构建 v1.0.2 |
| ✅ `release/ok-windows-arm64.exe` | 已构建 v1.0.2 |
| ✅ `release/ok-linux-amd64` | 已构建 v1.0.2 |
| ✅ `release/ok-linux-arm64` | 已构建 v1.0.2 |
| ✅ `release/ok-darwin-amd64` | 已构建 v1.0.2 |
| ✅ `release/ok-darwin-arm64` | 已构建 v1.0.2 |
| ❓ `release/ok-windows-desktop.exe` | 旧版，需 Wails 构建 |

如果只...
