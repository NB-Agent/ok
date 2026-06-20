---
key: episodic-18
type: episodic
created: 2026-06-20T19:52:17Z
source: auto-evolution
---

## Input
C:\Users\Administrator\Desktop\daima\ok-master>git remote set-url origin git@github.com:NB-Agent/ok.git

C:\Users\Administrator\Desktop\daima\ok-master>git push -u origin master
The authenticity of...

## Output
SSH 连上了 GitHub，但没有配 SSH key。安全提示是第一次连接的正常现象。

## 最简单的方案——用 Token + HTTPS

去 https://github.com/settings/tokens 创建一个 **token**（勾选 `repo` 权限），然后：

```cmd
git remote set-url origin https://github.com/NB-Agent/ok.git
git push -u origin master
```

提示输密码时，**粘贴你的 token 进去**（不是 GitHub 密码，输入时屏幕不会有任何变化，正常粘贴后回车就行）。

---

### 或者配一次 SSH key（以后就不用输密码了）

```cmd
ssh-keygen -t ed25519 -C "your-email@example.com"
```

一路回车。然后：

```cmd
type %USERPROFILE%\.ssh\id_ed25519.pub
```

复制输出的内容，粘贴到 https://github.com/...
