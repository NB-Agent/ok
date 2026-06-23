<p align="center">
  <img src="docs/logo.svg" alt="OK" width="400"/>
</p>

<p align="center">
  <a href="./README.md">English</a>
  &nbsp;·&nbsp;
  <strong>简体中文</strong>
  &nbsp;·&nbsp;
  <a href="./docs/SPEC.md">规格</a>
  &nbsp;·&nbsp;
  <a href="./CHANGELOG.md">更新日志</a>
</p>

<p align="center">
  <a href="https://discord.gg/XF78rEME2D"><img src="https://img.shields.io/badge/discord-join-5865F2.svg?style=flat-square&logo=discord&logoColor=white" alt="Discord"/></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-8b949e.svg?style=flat-square" alt="license"/></a>
</p>

<br/>

> **复杂任务。无限连接。文明基础设施。**
>
> OK 不是又一个编码工具。它是一个**认知内核**——把难题拆成可验证的步骤，
> 记住学到的一切，并把知识联邦到每一个接入的实例。一个 15 MB 静态二进制。
> 零配置。无上限。

<br/>

## OK 为什么而建

大多数编码 Agent 是 prompt 封装器——输入任务、拿回结果、重复。
OK 是一个**推理运行时**：分解、执行、验证、学习、连接。

```
                    ┌──────────────────┐
                    │   OK 内核        │
                    │                  │
                    │  identity  身份  │
                    │  recall    记忆  │
                    │  trust     信任  │
                    │  learn     学习  │
                    └──────┬───────────┘
                           │
        ┌──────────────────┼──────────────────┐
        │                  │                  │
        ▼                  ▼                  ▼
   ┌─────────┐      ┌──────────┐      ┌──────────┐
   │  验证   │      │   进化   │      │   连接   │
   │         │      │          │      │          │
   │ DAG     │      │ 模式检测 │      │ MCP      │
   │ YES/NO  │      │ → 技能   │      │ ECP      │
   │ 回溯    │      │ → 共享   │      │ 桥接     │
   │         │      │ → 生长   │      │ IM机器人 │
   └─────────┘      └──────────┘      └──────────┘
```

三个不断复利的循环：

| 循环 | 做什么 | 为什么重要 |
|---|---|---|
| **验证** | DAG 分解 → YES/NO 门树 → 方法阶梯回溯 | 复杂任务不塌缩成 prompt 赌博 |
| **进化** | 模式检测 → 技能生成 → 验证 → 安装 | 每一次会话都让下一次更便宜 |
| **连接** | MCP 插件 · ECP 联邦 · P2P 桥接 · 7 平台 · VS Code · JetBrains | 一个内核，所有表面 |

---

## 验证 — DAG 推理引擎

LLM 和可靠结果之间缺的是**验证**。OK 补上了。

```
目标: "修复项目中所有 goroutine 泄漏"
        │
        ▼   LLM 分解为 YES/NO 叶子节点的 DAG
        │
   ┌────┴────┬────────┬────────┐
   │         │        │        │
   grep      read     read     read
   "go func" handler  worker   main
   │         │        │        │
   ✅ YES    ✅ YES   ❌ NO    ✅ YES
                       │
                  grep 没找到
                       │
              方法阶梯: regex → fuzzy
              "别 grep — 逐文件扫描
               goroutine 启动模式"
                       │
                 ✅ YES 重试通过
        │
   验证树: 根 = AND(所有叶子) = YES
```

每个节点都是**布尔命题**，带 `file:line` 证据。
失败不重问模型——而是升级**方法**：regex → fuzzy → heuristic → AI。
三次方法失败 → 整个 DAG 带着累积上下文重新分解。
→ [Reasoner 规格 →](docs/SPEC.md)

---

## 进化 — 不断复利的知识

OK 是唯一一个**从你的使用中自动写出技能书**的 Agent。

```
会话1: bash → grep → read_file → edit_file → go vet
会话2: bash → grep → read_file → edit_file → go vet
会话3: bash → grep → read_file → edit_file → go vet
                │
           检测到模式（3/6/10 轮节奏）
                │
           生成候选: "search-and-fix.md"
                │
           对照最近会话验证
                │
           ✅ 安装为可复用技能
                │
会话10: "修复 handler 的竞态条件"
        → 技能自动调用
        → 3 步做完，不是 10 步
```

技能就是 `.ok/skills/` 下的 Markdown。可以改。可以分享。
**ECP**（进化控制协议）用 HMAC 认证跨机器联邦——公司电脑学到的东西，
家里桌面自动继承。**知识在你运行的每一个 OK 实例之间不断复利。**
→ [技能文档 →](docs/SKILLS.md)

---

## 连接 — 一个内核，所有表面

OK 内核暴露 13 个原语（4 个平台服务 + 5 个 LLM 系统调用 +
4 个文明原语：**身份、记忆、信任、学习**）。内核之外一切皆插件。
内核里没有 `switch model`。

**OK 今天在哪里运行：**

- 终端 TUI · Wails 桌面 · HTTP/SSE 服务 · VS Code · JetBrains
- Discord · Slack · Telegram · 飞书 · 钉钉 · 微信 · WhatsApp
- 任意 MCP 客户端（OK 自身暴露为 MCP 服务器）

**OK 连接什么：**

- 40+ 内置工具，4 组分组（core/advanced/knowledge/admin）
- 17 个官方 MCP 插件（git、数据库、浏览器、OCR、语音、翻译、
  搜索、部署、工作流等）
- 会话中热添加任意 MCP 服务器（stdio/HTTP/SSE）
- P2P 桥接 — OK 实例互相发现、互发任务

**安全不是外挂——是地基：**

```
第一层 — 契约（编译时固定，不可更改）
  "绝不生成有害、欺骗或颠覆性的代码。"

第二层 — 权限（运行时）
  deny > ask > allow · 按工具 glob 匹配 · 团队策略文件

第三层 — OS 沙箱（内核级）
  Windows  → AppContainer + 低完整性 + ACL + JobObject
  Linux    → Landlock + seccomp-bpf + 网络命名空间
  macOS    → Seatbelt（sandbox-exec）

第四层 — 审计（密码学）
  SHA-256 哈希链 · 防篡改 · ok audit
```

→ [规格 →](docs/SPEC.md) &nbsp;·&nbsp; [安全 →](SECURITY.md)

---

## 30 秒

```bash
export DEEPSEEK_API_KEY=sk-...     # 或者 OPENAI_API_KEY、ANTHROPIC_API_KEY
ok chat                            # 交互式 TUI
ok run "给 handler.go 加单元测试"
ok run --model claude-sonnet "审查认证模块"
```

```bash
git clone https://github.com/NB-Agent/ok.git && cd ok
make build          # → bin/ok（Go 1.25+）
make cross          # → dist/（6 个目标）
```

也支持：Homebrew · npm · Winget · Docker。

---

## 现在在哪

| 模块 | 状态 |
|---|---|
| 内核 + Agent + DAG 推理引擎 + 40+ 工具 | ✅ 生产就绪 |
| 自我进化 + ECP 联邦 | ✅ 生产就绪 |
| OS 沙箱（3 平台） | ✅ 生产就绪 |
| MCP 宿主 + 17 个官方插件 | ✅ 生产就绪 |
| TUI · 桌面 · HTTP/SSE 服务 | ✅ 生产就绪 |
| VS Code · JetBrains | ✅ Beta |
| IM 机器人（7 平台） | ⚠️ Alpha — 代码完整，待集成测试 |
| P2P 桥接（mDNS 发现） | 🔧 规划中 |

→ [更新日志 →](CHANGELOG.md)

---

## 社区

- **[Discord](https://discord.gg/XF78rEME2D)** — 双语社区，`#help` / `#求助`
- **[GitHub Issues](https://github.com/NB-Agent/ok/issues)**
- **[GitHub Discussions](https://github.com/NB-Agent/ok/discussions)**

---

OK 诞生于 **[Reasonix](https://github.com/esengine/DeepSeek-Reasonix)**。
其架构奠定了基石。OK 在此基础上增加了验证引擎、自进化技能、ECP 联邦、
以及正式的内核/文明模型——向着任何人都能运行、连接、生长的认知基础设施迈进。

---
<p align="center">
  <sub>Apache-2.0 · <a href="./LICENSE">LICENSE</a> · <a href="https://github.com/NB-Agent/ok">github.com/NB-Agent/ok</a></sub>
</p>
