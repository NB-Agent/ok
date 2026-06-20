---
key: episodic-39
type: episodic
created: 2026-06-20T20:30:09Z
source: auto-evolution
---

## Input
要等很久吗

## Output
不要等我在线等，直接去看看进度条：

## 查看构建进度

https://github.com/NB-Agent/ok/actions

点进去能看到两个 job 在跑：

| Job | 时间 |
|-----|------|
| 🟡 `goreleaser` | 交叉编译 6 平台 → 约 3-5 分钟 |
| 🟡 `desktop` | 安装 pnpm + 前端构建 + Wails 编译 → 约 3-5 分钟 |

**两个同时跑，总时间大约 5-7 分钟。** 等黄色圈圈变绿勾勾，刷新 Release 页面就能看到所有二进制了。
