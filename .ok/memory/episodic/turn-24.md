---
key: episodic-24
type: episodic
created: 2026-06-20T20:02:09Z
source: auto-evolution
---

## Input
我自己设置好了域名，404
这里没有GitHub Pages网站。

如果你想发布 GitHub Pages，请阅读完整文档，了解如何为你的仓库、组织或用户账户设置 GitHub Pages。

GitHub状态 — @githubstatus

## Output
好了！现在需要两步：

## 1️⃣ 推送新首页

```cmd
cd C:\Users\Administrator\Desktop\daima\ok-master
git add docs/index.html
git commit -m "docs: add GitHub Pages landing page"
git push
```

## 2️⃣ 开启 GitHub Pages

去你的 GitHub 仓库 https://github.com/NB-Agent/ok

**Settings → Pages** → 在 **Branch** 下拉选 `master`，文件夹选 `/docs` → **Save**

![GitHub Pages settings](https://docs.github.com/assets/cb-77564/mw-1440/images/help/pages/getting-started-with-github-pages-config-branch.webp)

等一两分钟，你的域名 `nbyyds.com` 就会指向这个漂亮的首...
