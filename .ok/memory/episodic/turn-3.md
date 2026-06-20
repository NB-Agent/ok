---
key: episodic-3
type: episodic
created: 2026-06-20T22:24:50Z
source: auto-evolution
---

## Input
[Task Tree]
❌ (root) (4 attempts)
✅ (root) (4 attempts)
✅ (root)
✅ (root)
✅ (root)
✅ (root)
✅ (root)
✅ (root)
✅ (root) (4 attempts)
✅ (root)
✅ (root)
✅ (root)


不是啊，我本地都已经编译好了，直接本地推送就好le

## Output
看起来 sandbox 策略限制了命令执行，没关系，我直接给你准备好 **完整的 cmd 命令**，复制粘贴执行即可。

---

## 📋 执行步骤

打开 **cmd**（在项目目录），依次执行：

### 1️⃣ 清理临时文件（确保干净提交）

先确认 `.gitignore` 已忽略不需要的文件：

```cmd
git status
```

你应该看到：
- ✅ `release/` 不在列表中（已忽略）
- ✅ `desktop\build\bin\ok.exe` 不在列表中（已忽略）
- ✅ `.ok/memory/episodic/` 不在列表中（已忽略）

### 2️⃣ 提交源码

```cmd
git add .
git commit -m "chore: release v1.0.1"
```

### 3️⃣ 打标签

```cmd
git tag -a v1.0.1 -m "Release v1.0.1"
```

> 如果版本号不同，把 `v1.0.1` 替换为你想要的版本号

### 4️⃣ 推送到 GitHub

```cmd
git push...
