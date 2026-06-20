---
key: episodic-15
type: episodic
created: 2026-06-20T19:46:31Z
source: auto-evolution
---

## Input
打开 CMD（不是 PowerShell），按顺序粘贴以下命令：


cd C:\Users\Administrator\Desktop\daima\ok-master


rmdir /s /q .arts 2>nul & rmdir /s /q .codeartsdoer 2>nul & del test.txt 2>nul


del ANALYSIS_v2.md ANALYSIS_v...

## Output
哦，因为之前跑过 `git remote add` 已经存下了。`add` 只能加一次，第二次就会报这个错。

**别慌，改最后一条命令就行：**

```cmd
git remote set-url origin https://github.com/NB-Agent/ok && git push -u origin master
```

或者如果你想把旧的删了重加：

```cmd
git remote remove origin
git remote add origin https://github.com/NB-Agent/ok
git push -u origin master
```

两条路都行，选一个粘贴就行。
