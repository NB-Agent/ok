# DST v2 架构设计

## 0. 当前版本的问题（为什么 v1 没用）

```
当前链路：
Agent写文件 → PostToolUse(编译检查/快照) → asyncDSTVerify(PCVA+Game+Judge)
                                             ↑
                                    LLM往返10s+，Agent已进入下一轮
                                    失败只注入一条notice，Agent可选忽略
                                    裁判和Agent是同一个LLM session
```

三个致命缺陷：
1. **异步→无强制力**：Agent 已经写了下一个文件，回滚会破坏新文件
2. **同模自评→无独立判断**：`WireLLM` 传的是同一个 provider，同一个 API key，Agent 的盲区 = 裁判的盲区
3. **迟到→信息过时**：验证完成时文件状态已变，Judge 读到的代码不是被改动的那个版本

## 1. 设计原则

| 原则 | 说明 |
|---|---|
| **同步阻断** | 验证不通过，Agent 不得进入下一步工具调用 |
| **独立裁判** | 验证使用独立 LLM session/temperature，不与 Agent 共享上下文 |
| **强制回滚** | 失败 = 文件回滚 + 失败报告注入 Agent 下条消息 |
| **分层验证** | 便宜的先跑，贵的后跑，绝大多数写入在 L0 就通过 |
| **回合级聚合** | 不在每个 write 后阻塞，在 PostToolUse 聚合到回合结束时统一裁决 |

## 2. 分层验证架构

```
┌──────────────────────────────────────────────────────┐
│                    Agent 回合                          │
│  write_file(A) → write_file(B) → bash test → ...     │
│  每步: PreToolUse(快照) → 执行 → PostToolUse(不验证)  │
└──────────────────────┬───────────────────────────────┘
                       │ 回合结束
                       ▼
┌──────────────────────────────────────────────────────┐
│ Layer 0: 编译/测试 （同步，~2-10s）                     │
│   go build ./... && go test ./...                     │
│   FAIL → 回滚到回合前快照 → 注入失败报告 → Agent重试    │
│   PASS → 继续                                          │
├──────────────────────────────────────────────────────┤
│ Layer 1: 原子提取 （同步，~3-8s）                       │
│   独立 LLM 分解需求 → [是/否原子] × N                   │
│   每个原子带 Verification (文件/命令/模式)              │
├──────────────────────────────────────────────────────┤
│ Layer 2: 原子逐个验证 （同步，~3-8s/原子）               │
│   对每个原子：                                          │
│     a. 有Verification → 执行命令/检查文件 → YES/NO     │
│     b. 无Verification → 独立LLM读代码→判断             │
│   FAIL → 回滚 → 注入"原子X未满足"报告                  │
│   ALL PASS → 回合成功，不可逆提交                       │
├──────────────────────────────────────────────────────┤
│ Layer 3: 博弈检查 （可选，~5-15s）                      │
│   独立 LLM 对改动做攻击/防御 → Judge 裁决               │
│   默认关闭，/dst deep 开启                              │
│   仅用于高风险改动（安全相关/核心逻辑）                  │
└──────────────────────────────────────────────────────┘
```

## 3. 核心数据结构

```go
// Verdict 替代当前的松散 bool+string 返回值
type Verdict struct {
    Passed    bool
    Layer     int           // 哪层失败的
    Reason    string        // 人类可读的失败原因
    AtomID    string        // 失败关联的原子ID
    FilePaths []string      // 需要回滚的文件
    FixHint   string        // 给 Agent 的修复建议
}

// TurnGuard 是回合级的验证门
type TurnGuard struct {
    // 独立 LLM 会话（不和 Agent 共用 session/messages）
    verifierLLM  provider.Provider
    verifierSess *agent.Session  // 短生命周期，每次回合重建

    // 快照：回合开始时的文件状态
    snapshots map[string][]byte

    // 当前回合收集的信息
    modifiedFiles []string
    atoms         []*core.Atom

    // 配置
    deepVerify bool  // 是否开启 Layer 3
    workDir    string
}
```

## 4. 执行流程（伪代码）

```go
// Agent 的 executeOne 改造
func (a *Agent) executeOne(ctx context.Context) (bool, error) {
    // ... LLM 选择工具 ...
    
    // PreToolUse: 快照每个将被写入的文件
    guard.Snapshot(toolName, args)
    
    // 工具执行
    result, err := tool.Execute(ctx, args)
    
    // PostToolUse: 不验证，只记录
    guard.RecordWrite(toolName, args, result)
    // ← 不再调用 asyncDSTVerify
    
    // ... 继续下一个工具调用 ...
}

// 回合结束：runTurn 完成后
func (a *Agent) runTurn(ctx context.Context, input string) error {
    // ... 原有逻辑 ...
    
    for executeOne returns tools continue {
        // 原有循环
    }
    
    // 回合结束 → 同步验证
    if guard != nil {
        verdict := guard.Verdict(ctx)
        if !verdict.Passed {
            guard.Rollback()
            // 注入失败上下文到下一条 user message
            a.session.Add(provider.Message{
                Role: provider.RoleUser,
                Content: fmt.Sprintf(
                    "[VERIFICATION FAILED at layer %d]\n%s\n\n"+
                    "Affected atoms: %s\n"+
                    "Rolled back files: %s\n\n"+
                    "Fix the issues and retry.",
                    verdict.Layer, verdict.Reason,
                    verdict.AtomID,
                    strings.Join(verdict.FilePaths, ", "),
                ),
            })
            // 递归：Agent 收到失败报告后自动重试
            return a.runTurn(ctx, input)
        }
        // PASS → 清空快照，不可逆
        guard.Commit()
    }
    return nil
}
```

## 5. 独立裁判设计

```
裁判 LLM 特征：
  - 独立 session（不与 Agent 共享 Messages）
  - temperature = 0（精确判断，无创意）
  - system prompt 不含 Agent 的工作方法论
  - system prompt 内容：
    "You are a verification engine. Your ONLY job is to judge
     whether a claim about the codebase is TRUE or FALSE.
     You have no context about what the agent intended.
     You see ONLY: the claim, the current file contents, and
     the diff since last turn. Answer YES or NO."
  - 不调用工具，纯文本判断
  - 超时 10s（比 Agent 的大模型调用短，因为是简单判断）
```

## 6. 原子提取改造

```
当前：从 LLM 响应中解析 markdown/todo_write
问题：Agent 可能不输出这些，或输出不规范的格式

v2 设计：
  回合结束时，由裁判 LLM 独立分解：
  
  输入：用户原始需求 + 当前回合的 diff
  输出：结构化原子列表
  
  System prompt:
  "Given a requirement and a code diff, decompose the
   requirement into 3-8 YES/NO propositions that can be
   verified by examining the code. Each proposition must
   reference a specific file and a specific claim.
   
   Format:
   - FILE: claim (verifiable by: read_file | go_build | go_test)
   
   Example:
   - internal/boot/boot.go: Coordinator receives DSTRunner not raw Agent
     (verifiable by: read_file)"
```

## 7. API 成本估算

| 层级 | LLM 调用 | Token 消耗 | 频率 |
|---|---|---|---|
| L0 编译/测试 | 0 | 0 | 每回合 |
| L1 原子提取 | 1 次（~500 in / ~200 out） | ~700 | 每回合 |
| L2 原子验证 | N 次（N=原子数，~300 in / ~50 out/个） | ~350N | 每回合 |
| L3 博弈检查 | 2N 次（攻击+防御，~500×2N） | ~1000N | 仅 deep 模式 |

典型回合（5 个原子，L0-L2）：
- 额外 LLM 调用：6 次
- 额外 token：~2500
- 额外延迟：~15-30s（可并行 L2 原子验证）
- 费用（DeepSeek flash，¥1/百万token）：~¥0.0025/回合

## 8. 可配置项

```toml
[dst]
enabled = true           # 总开关
deep    = false          # L3 博弈检查
max_atoms = 8            # 每回合最多原子数
timeout  = "30s"         # 单层超时
model    = ""            # 裁判模型，空=使用 Agent 同 provider 的 flash 模型
                         # 推荐设为独立的便宜模型，如 "deepseek-flash"
```

## 9. 实现路线（增量，不重写）

| 阶段 | 内容 | 改动文件 |
|---|---|---|
| Phase 1 | TurnGuard 数据结构 + 快照/回滚引擎 | `internal/dstvalid/guard.go` (新) |
| Phase 2 | 回合结束同步钩子 + 失败注入 | `internal/agent/agent.go` |
| Phase 3 | L0 编译/测试检查（已有，迁移） | `internal/dstvalid/hooks.go` → guard |
| Phase 4 | L1 独立原子提取 | `internal/dstvalid/atomize.go` (新) |
| Phase 5 | L2 原子验证（独立 LLM） | `internal/dstvalid/verify.go` (新) |
| Phase 6 | L3 博弈检查（可选） | `internal/dstvalid/game.go` (迁移) |
| Phase 7 | 配置项 + /dst 命令改造 | `internal/config/`, `internal/control/` |

## 10. 与当前版本的关键差异

| | v1 (当前) | v2 (设计) |
|---|---|---|
| 验证时机 | 异步，Agent 已进入下一步 | 同步，回合结束时阻断 |
| 验证模型 | 同 Agent 同 session | 独立 session，独立 temperature |
| 失败处理 | notice 提醒，Agent 可选操作 | 强制回滚 + 注入失败报告 + 自动重试 |
| 原子来源 | 解析 Agent 输出 | 独立 LLM 分解 |
| bash 限制 | 白名单，降低能力 | 无白名单，由快照+回滚保证安全 |
| 非 Go 项目 | 编译检查跳过 | L1+L2 不依赖语言，完全可用 |
