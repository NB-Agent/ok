---
key: episodic-42
type: episodic
created: 2026-06-20T20:35:36Z
source: auto-evolution
---

## Input
[Task Tree]
❌ (root)
✅ (root)
✅ (root)
✅ (root) (4 attempts)
✅ (root) (4 attempts)
✅ (root) (5 attempts)
✅ (root)
✅ (root)
✅ (root) (3 attempts)
✅ (root) (3 attempts)
✅ (root)
✅ (root)
✅ (root) (2 ...

## Output
删掉旧的桌面 workflow（已合并到 release.yml），一起推送：

```cmd
cd C:\Users\Administrator\Desktop\daima\ok-master
del .github\workflows\release-desktop.yml
git add .
git commit -m "ci: replace goreleaser with manual cross-compile, merge desktop into release workflow"
git push
```

然后删旧 tag 重新打：

```cmd
git tag -d v1.0.1
git push origin --delete v1.0.1
git tag v1.0.1
git push origin v1.0.1
```

这次流程是：

| Job | Runner | 时间 | 产物 |
|-----|--------|------|------|
| `cli` × 6 | ubuntu-latest (并行) | ~2分钟 | `ok-linu...
