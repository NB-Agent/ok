---
key: episodic-30
type: episodic
created: 2026-06-21T09:22:11Z
source: auto-evolution
---

## Input
二进制也需要改吧

## Output
不需要手动改二进制。**源码就是一切**——重新编译就会自动带上修复：

```powershell
go build -o ok.exe .
```

编译完的 `ok.exe` 就包含了新逻辑。二进制是源码的产物，不是独立的东西。

所以流程不变：**先提交源码 push**，然后 `go build` 重编译即可。继续：

```powershell
git add internal/boot/boot.go internal/memory/store.go internal/memory/shared.go
git commit -m "fix: cap shared memory to prevent prefix bloat — index truncated to 30 entries, max 100 files"
git push origin master
```
