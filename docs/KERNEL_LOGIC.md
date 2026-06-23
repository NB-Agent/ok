# AI 代理内核 — 问题求解逻辑

> 本文档描述 OK Agent 内核的**抽象问题求解逻辑**，与 `DST_KERNEL_DESIGN.md`
> 互补——那里是实际实现（ToolHooks/DSTHooks/ProofChain），这里是
> **递归分解→验证驱动→证据聚合** 的推理引擎。

---

## 总体架构

```
┌──────────────────────────────────────────────────────────────────┐
│                         AI 代理内核                              │
│                                                                  │
│  ① 输入任务                                                        │
│     │                                                              │
│     ▼                                                              │
│  ② 感知 GAP                                                       │
│     ├─ 感知当前状态（只读工具 → 获取事实）                         │
│     ├─ 定义目标状态（用户期望的结果）                               │
│     └─ 识别差距（当前 - 目标 = 待解决问题）                        │
│                                                                  │
│  ③ 分解                                                           │
│     └─ 递归将差距分解为是/否子问题                                 │
│        直至每个叶子 = 一个原子动作（读文件、跑命令、检查变量等）    │
│                                                                  │
│  ════════════════════ 中线 ════════════════════════════════════    │
│  LEFT（分析）                    │  RIGHT（执行）                   │
│  · 只读工具：grep/read_file/     │  · 写工具：write_file/          │
│    glob/web_fetch/search         │    edit_file/bash/desktop       │
│  · 验证子代理（task()）          │  · P→C→E→V 循环                │
│  · 证据收集与聚合                │                                 │
│  ═══════════════════════════════════════════════════════════════    │
│                                                                  │
│  ▸ 中线是设计约定，不是运行时强制访问控制。实际执行层面由以下      │
│    机制 enforce：                                                │
│    · Plan Mode → Gate 拦截所有写工具（write/edit/bash）          │
│    · task() 子代理可选只读工具白名单 → 验证子代理无法写文件      │
│    · executeBatch 中只读工具可并行、写工具串行 → 天然序列化      │
│                                                                  │
│  ④ 叶子验证（LEFT 侧）                                            │
│     ┌─────────────────────────────────────────┐                   │
│     │  并行扇出：每个叶子=一个独立验证子代理     │                  │
│     │  task("检查 X 是否存在") → YES/NO + 证据  │                  │
│     │  task("检查 Y 是否正确") → YES/NO + 证据  │                  │
│     │  task("检查 Z 是否通过") → YES/NO + 证据  │                  │
│     └──────┬──────────┬──────────┬────────────┘                   │
│            ▼          ▼          ▼                                │
│      证据:path:line  证据:path:line  证据:path:line                │
│            │          │          │                                │
│            └──────────┴──────────┴────────────────┐               │
│                                                    ▼               │
│  ⑤ 父聚合（后序验证）                                             │
│     父 = 子1 AND 子2 AND 子3 ...                                    │
│     全部为"是" → 父为"是" → 向上聚合                                │
│     任一为"否" → 回溯到根因叶子 → 重新验证                        │
│                                                    │               │
│  ⑥ 根节点判断                                                    │
│     根 = "是" → 输出"问题解决" + 证据链摘要         │               │
│     根 = "否" → 回到 ② 感知（迭代）                 │               │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

---

## 核心循环对比

### 原版（每个原子内部的 P→C→E→V）

```
感知 → 认知 → 执行 → 验证 → (否 → 回到感知)
```

RIGHT 侧的子代理使用这个循环来**改变环境**：

```
┌─────────────────────────────────┐
│  P(erceive)                     │
│    └─ 读取当前文件/状态          │
│         ↓                        │
│  C(ognize)                      │
│    └─ 推理：从 A → B 需要做什么  │
│         ↓                        │
│  E(xecute)                      │
│    └─ write_file/edit_file/bash │
│         ↓                        │
│  V(erify)                       │
│    └─ 检查执行结果是否符合预期   │
│         ↓ (否)                   │
│  回到 P（迭代）                  │
└─────────────────────────────────┘
```

### 验证器（LEFT 侧每个叶子的验证）

```
read(证据) → compare(标准) → decide(YES/NO) → output(evidence)
```

验证器**没有执行能力**——它只读、比较、输出。

---

## 验证标准（每个叶子必须输出）

```
YES/NO — <证据类型> <具体位置>
```

| 证据类型 | 格式示例 |
|---------|---------|
| 文件内容 | `file:internal/agent/agent.go:42` |
| grep 命中 | `grep:"Validate" in internal/agent/*.go:15` |
| 命令输出 | `bash:"go build ./..." → exit 0` |
| glob 存在 | `glob:internal/kernel/*.go → 3 files` |
| API 响应  | `web_fetch:https://... → status 200` |

---

## 树结构示例

```
任务：实现新命令 "ok audit"
│
├── [感知] 现有代码结构？（读目录）
│   └── YES — cmd/ok/main.go, internal/commands/
│
├── [验证] 是否有类似命令可参考？
│   └── YES — cmd/ok/main.go:42 — "ok verify" 实现模式
│
├── [分解] 需要哪些文件？
│   ├── cmd/ok/main.go — 注册子命令         ← 叶子：exec
│   ├── internal/commands/audit.go — 实现    ← 叶子：exec
│   ├── internal/commands/audit_test.go — 测试 ← 叶子：exec
│   └── 编译通过 + 测试通过                   ← 叶子：verify
│
├── [执行] 写 cmd/ok/main.go 注册
│   └── [验证] grep "audit" cmd/ok/main.go → YES :line:55
│
├── [执行] 写 internal/commands/audit.go
│   └── [验证] go build ./... → YES :exit 0
│
├── [执行] 写 internal/commands/audit_test.go
│   └── [验证] go test ./internal/commands/ → YES :exit 0
│
└── [聚合] 根 = 是 → 输出证据链
```

---

## 不变约束

1. **中线不可逾越** — LEFT 分析不做写操作，RIGHT 执行不做只读验证
2. **后序验证** — 子节点全为"是"后，父节点才可能为"是"
3. **证据不可省** — 每个 YES/NO 必须附带 concrete evidence
4. **扁平均摊** — 所有叶子扁平扇出，主 agent 独自聚合（无中间聚合节点）
5. **覆盖检查** — 扇出前必须自问："所有这些子问题为"是"时，原问题是否被完全回答？"
6. **零 LLM 验证** — 优先使用编译/测试/grep/bash 等确定性验证，避免异步 LLM 调用

---

---

## 外层架构：六层洋葱

从内核向外，每层只依赖内层：

```
┌─────────────────────────────────────────────────────────────────────┐
│  L6  EXTERNAL                                                       │
│  ┌─────────────────────────────────────────────────────────────────┐│
│  │  L5  FRONTENDS                                                  ││
│  │  ┌─────────────────────────────────────────────────────────────┐││
│  │  │  L4  PLUGINS (MCP Servers)                                  │││
│  │  │  ┌─────────────────────────────────────────────────────────┐│││
│  │  │  │  L3  TOOL REGISTRY (54+ 内置工具)                       ││││
│  │  │  │  ┌─────────────────────────────────────────────────────┐││││
│  │  │  │  │  L2  CONTROLLER (传输无关主循环)                    │││││
│  │  │  │  │  ┌─────────────────────────────────────────────────┐│││││
│  │  │  │  │  │  L1  AGENT (运行循环 + DST 验证)               ││││││
│  │  │  │  │  │  ┌─────────────────────────────────────────────┐││││││
│  │  │  │  │  │  │  L0  KERNEL (不可变核心)                   │││││││
│  │  │  │  │  │  │  · 4 Platform Services                     │││││││
│  │  │  │  │  │  │  · 5 LLM Syscalls                          │││││││
│  │  │  │  │  │  │  · 4 Civilization Primitives               │││││││
│  │  │  │  │  │  └─────────────────────────────────────────────┘││││││
│  │  │  │  │  └─────────────────────────────────────────────────┘│││││
│  │  │  │  └─────────────────────────────────────────────────────┘││││
│  │  │  └─────────────────────────────────────────────────────────┘│││
│  │  └─────────────────────────────────────────────────────────────┘││
│  └─────────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────────┘
```

### L0 — Kernel（不可变核心）

**包**: `internal/kernel/`

12 个组件，零值安全（懒初始化）：

| 组 | 接口 | 职责 |
|----|------|------|
| **Platform Services** | `Sandbox` | 命令隔离执行（appcontainer/Landlock/seccomp/Docker） |
| | `Session` | 对话状态管理、压缩 |
| | `Provider` | LLM 连接（模型无关） |
| | `Controller` | 传输无关的会话驱动器 |
| **LLM Syscalls** | `Bash` | 通用执行通道 |
| | `ReadFile` | 精确文件读取 |
| | `WriteFile` | 精确文件写入 |
| | `EditFile` | 精确字符串替换 |
| | `Grep` | 正则搜索 |
| **Civilization** | `Identity` | 用户画像：角色/偏好/本地化 |
| | `Recall` | 跨 session 长期记忆 |
| | `Trust` | 证据链：防篡改校验 |
| | `Learn` | 自我进化：提取→生成→验证→发布 |

**Kernel 的 Schema() 只暴露 9 个工具**（5 syscalls + 4 civ primitives）。
一切其他工具都是插件。

### L1 — Agent（运行循环）

**包**: `internal/agent/`

```
Agent.Run() {
    for step = 0 .. maxSteps {
        stream() → LLM 输出 text + toolCalls[]
        if len(calls)==0 → 最终回答，返回

        executeBatch(calls) {
            for each call:
                executeOne(call) {
                    PreToolUse   (快照/白名单，可 block)
                    tool.Execute(...)
                    PostToolUse  (go build + go test，不可 block)
                    ConsumeRollback → 失败回滚 + 修改 result
                }
            return results[]
        }
        session.Add(tool results)
    }
}
```

- 包裹 Kernel，添加 `ToolHooks`（DST 验证链）
- 管理 session、usage tracking、plan mode、tool cache

### L2 — Controller（传输无关主循环）

**包**: `internal/control/`

- `Compose()` — 每次轮次前注入：memory、skills、OK.md、turn tail
- `input.go` — 用户输入处理（unix unicode fix、slash 命令展开）
- `controller_turn.go` — 单轮执行（approval、memory、agent.Run）
- `controller_query.go` — 快速查询模式
- `controller_approval.go` — 操作审批流
- `controller_memory.go` — 记忆注入
- `refs.go` — `@ref` 引用解析
- `slash.go` — `/` 快捷命令

**所有前端共享同一个 Controller** — 行为统一。

### L3 — Tool Registry（54+ 内置工具）

**包**: `internal/tool/`

工具分组：

| 组 | 典型工具 | schema tokens |
|----|---------|:------------:|
| `core` | bash, read_file, write_file, edit_file, grep, glob, ls, task | ~30% |
| `advanced` | git, database, debug, deploy, desktop, browser, ocr, translate, voice, schedule, undo, todo | ~40% |
| `knowledge` | rag, semantic-search, web_fetch, repo | ~15% |
| `admin` | capabilities, covenant, self_scan, ok-verify, style_check, vuln_check, go_profile | ~15% |

**Registry.ActivateGroups("core")** 只暴露核心工具，节省 ~70% schema tokens。

### L4 — Plugins（MCP 服务器）

**包**: `internal/plugin/` + `plugins/*/`

```
plugins/
├── ok-ai-vision/       # 图像/视频分析
├── ok-browser/         # 无头浏览器
├── ok-computer-use/    # 桌面操控
├── ok-database/        # SQLite/PostgreSQL/MySQL
├── ok-debug/           # Delve 调试器
├── ok-deploy/          # SSH 部署
├── ok-desktop/         # 桌面自动化
├── ok-digest/          # 哈希/编码
├── ok-git/             # Git 操作
├── ok-ocr/             # 文字识别
├── ok-repo/            # 多仓库管理
├── ok-search/          # RAG 搜索
├── ok-translate/       # 翻译
├── ok-utils/           # 工具集
├── ok-voice/           # 语音输入/输出
├── ok-wake-word/       # 唤醒词
├── ok-web-fetch/       # HTTP 抓取
├── ok-workflow/        # 工作流
└── registry-server/    # 插件注册中心
```

支持三种传输：
- **stdio** — 子进程 stdin/stdout
- **HTTP/SSE** — 远程服务
- **WebSocket** — 双向实时

### L5 — Frontends（用户入口）

```
cmd/
├── ok/                # CLI/TUI 主入口
│   └── main.go
├── ok-slack-bot/      # Slack 机器人
├── ok-discord-bot/    # Discord 机器人
├── ok-feishu-bot/     # 飞书机器人
├── ok-telegram-bot/   # Telegram 机器人
├── ok-wechat-bot/     # 微信机器人
├── ok-whatsapp-bot/   # WhatsApp 机器人
├── ok-dingtalk-bot/   # 钉钉机器人
└── ok-installer/      # 安装器

desktop/
├── main.go            # Wails 桌面应用
├── app.go             # 桌面控制器
├── frontend/          # React TypeScript UI
└── wire.go            # 依赖注入

internal/serve/
├── serve.go           # HTTP/SSE 服务端
├── hcp_ws.go          # WebSocket 端点
├── auth.go            # API key 认证
├── wire.go            # 依赖注入
└── broadcaster.go     # Server-Sent Events 广播
```

### L6 — External（外部生态）

```
用户层：
├── ok.toml            # 项目配置
├── OK.md              # 项目记忆（提交版本）
├── OK.local.md        # 个人记忆（git-ignore）
├── ~/.config/ok/OK.md # 用户全局记忆
└── .ok/skills/        # 可复用技能/playbook

外部服务：
├── LLM APIs           # DeepSeek/OpenAI/Anthropic/etc.
├── MCP 远程服务器      # 第三方插件
├── Git 远程仓库        # 源码托管
└── CI/CD 管线          # GitHub Actions
```

---

## 与现有实现的关系

```
抽象逻辑层（本文档）           →   实现层（DST_KERNEL_DESIGN.md）
─────────────────────────         ──────────────────────────────
GAP 感知 + 分解                  →   Controller.Compose() + Agent 主循环
LEFT 验证子代理（只读）          →   task() 子代理（read-only tools）
RIGHT 执行循环（P→C→E→V）       →   Agent.Run() + executeOne()
每个叶子后的验证                →   PostToolUse (go build + go test)
证据链                          →   ProofChain (SHA-256 哈希链)
回溯重试                        →   ConsumeRollback (回滚 + 修改 result)
                                  + Planner.Execute (重试失败任务)
聚合                            →   Reasoner.Aggregate()
```

### 未实现的抽象层概念

| 概念 | 状态 | 原因 |
|------|:--:|------|
| **上溯回溯**（父节点重评估） | ❌ 未实现 | 当前只做叶子级别的验证和重试（DL0/L1）。聚合后的整体正确性验证需要 LLM 判定，与"零 LLM 验证"原则冲突。未来可通过可选验证钩子实现。 |
| **Kernel 作为运行时组合根** | ⚠️ 蓝图 | `kernel.Kernel` 结构体是架构蓝图而非运行时组合根——实际组合在 `internal/boot/boot.go` 中完成。Controller 存储 `Kernel` 字段供前端读取文明原语（Identity/Recall/Trust/Learn）。 |
| **Reasoner 分解上下文** | ⚠️ 透传 | Reasoner.decompose() 接收的是 Compose() 处理后的完整上下文（含 memory 更新、proof chain、env 诊断等）。分解 LLM 能读到所有信息但夹杂噪声。当前工作正常，后续可剥离纯目标传递给分解器。 |
