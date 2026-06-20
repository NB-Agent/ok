---
key: episodic-9
type: episodic
created: 2026-06-20T19:24:42Z
source: auto-evolution
---

## Input
Microsoft Windows [版本 10.0.19045.6466]
(c) Microsoft Corporation。保留所有权利。

C:\Users\Administrator>cd C:\Users\Administrator\Desktop\daima\ok-master

C:\Users\Administrator\Desktop\daima\ok-master>se...

## Output
好了，那个 `.bat` 文件编码乱了。改用 PowerShell 脚本：

## 操作

**在 `C:\Users\Administrator\Desktop\daima\ok-master` 文件夹中，右键 → "在终端中打开"，然后粘贴：**

```powershell
.\setup.ps1
```

或者直接**右键 `setup.ps1` → "使用 PowerShell 运行"**。

脚本会：

1. 🧹 清理 .arts/ .codeartsdoer/ 以及所有旧历史文档
2. 📦 `git init` → `git add -A` → `git commit`
3. 🚀 `git push` 到 `https://github.com/NB-Agent/ok`

另外，你找到 GitHub 的删除页面了吗？如果还没删仓库，推送到不存在的仓库会失败。需要先删掉旧的再跑脚本。
