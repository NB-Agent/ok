---
key: episodic-3
type: episodic
created: 2026-06-21T12:39:57Z
source: auto-evolution
---

## Input
优化了很多东西，是需要代码全部推送吗，还是说只是推送修改过的代码

## Output
在 PowerShell 里直接运行即可，推送的只有**源代码修改**，没有二进制：

```powershell
.\push-github.bat
```

或者手动：

```powershell
git rm --cached -r .ok/memory/episodic/
git add -A
git commit -m "chore: 优化代码结构，清理会话记忆跟踪"
git push origin master
```

> `release/` 7 个二进制在 `.gitignore` 里，不会推送。只提交 ~40 个源码文件的修改。
