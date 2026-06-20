---
key: episodic-36
type: episodic
created: 2026-06-20T20:25:19Z
source: auto-evolution
---

## Input
[Task Tree]
❌ (root)
✅ (root) (2 attempts)
✅ (root)
✅ (root) (3 attempts)
✅ (root) (3 attempts)
✅ (root) (10 attempts)
✅ (root) (5 attempts)
✅ (root)
✅ (root) (2 attempts)
✅ (root)
✅ (root)
✅ (root...

## Output
好了。现在推上去。

```cmd
cd C:\Users\Administrator\Desktop\daima\ok-master
git add .
git commit -m "ci: add desktop release workflow, fix Go version and repo refs"
git push
```

---

## 以后发版流程

只需要一行命令：

```cmd
git tag v1.0.1 && git push origin v1.0.1
```

GitHub Actions 会自动并行跑两个 job：

| Job | Runner | 产物 |
|-----|--------|------|
| `goreleaser` | ubuntu-latest | CLI 二进制 × 6 平台 |
| `desktop` | windows-latest | `ok.exe`（Wails 桌面版） |

全自动上传到 Release 页面，用户直接下载。
