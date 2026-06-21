---
key: episodic-24
type: episodic
created: 2026-06-21T09:11:47Z
source: auto-evolution
---

## Input
rosoft Windows [版本 10.0.19045.6466]
(c) Microsoft Corporation。保留所有权利。

C:\Users\Administrator>dir $env:APPDATA\ok\shared\memory\shared-*.md | measure
'measure' 不是内部或外部命令，也不是可运行的程序
或批处理文件。

C:\Users...

## Output
啊，你还在 CMD 里！`Measure-Object` 是 PowerShell 命令。改用 CMD 语法：

先切换到 PowerShell：
```
powershell
```

等提示符变成 `PS` 开头后，再跑：

```powershell
dir $env:APPDATA\ok\shared\memory\shared-*.md | Measure-Object | Select-Object -ExpandProperty Count
```
