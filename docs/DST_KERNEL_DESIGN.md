# DST 内核 — 将验证写入 Agent 主循环

## 0. 当前 Agent 内核

```
Run() {
    session.Add(user message)
    for step = 0 .. maxSteps {
        stream() → LLM 输出 text + toolCalls[]
        if len(calls)==0 → 最终回答，返回

        executeBatch(calls) {
            for each call:
                executeOne(call) {
                    PreToolUse  (快照/白名单，可 block)
                    tool.Execute(...)
                    PostToolUse (编译检查+测试检查，不可block)
                    ConsumeRollback → 修改 result
                }
            return results[]
        }

        session.Add(tool results)   ← 模型在下一步会读到这些结果
        maybeCompact()
    }
}
```

## 1. 核心设计：工具结果就是 Agent 的"感知通道"

Agent 不"看"屏幕。它只看 `session.Messages` 中的 tool result。
编译/测试失败时，`ConsumeRollback` 修改 result 为 `"rolled back: compile failed\nerror:\n..."`。

**把验证失败写成 tool result，Agent 就会无条件读到。** 不需要额外的 user 消息。

## 2. 实际实现（DST v2 简化后）

### 2.1 ToolHooks 接口（当前代码）

**文件**: `internal/agent/agent.go:96`

```go
type ToolHooks interface {
    PreToolUse(ctx, name, args) (block bool, message string)
    PostToolUse(ctx, name, args, result string)
    ConsumeRollback() (reason, detail string, happened bool)
}
```

`ConsumeRollback` 是 `ToolHooks` 的直接方法——不再需要 `RollbackReporter` 类型断言。

### 2.2 executeOne — 单次工具调用内的验证

**文件**: `internal/agent/agent.go:561`

```go
func (a *Agent) executeOne(ctx context.Context, call provider.ToolCall) toolOutcome {
    // ... 权限检查、PreToolUse ...
    result, err := t.Execute(tctx, json.RawMessage(call.Arguments))

    // PostToolUse：编译检查 + 测试检查
    if a.hooks != nil {
        a.hooks.PostToolUse(ctx, call.Name, json.RawMessage(call.Arguments), result)
        // ConsumeRollback: 失败后立即回滚并注入失败信息
        if reason, detail, happened := a.hooks.ConsumeRollback(); happened {
            result = fmt.Sprintf("rolled back: %s failed\nerror:\n%s", reason, detail)
        }
    }
    // ...
}
```

### 2.3 DSTHooks — 同步编译/测试检查

**文件**: `internal/dstvalid/hooks.go`

- `PreToolUse`: 文件快照（`snapshot.Capture(path)`）
- `PostToolUse`: `go build ./...` → `go test ./...` → 失败时调用 `rollbackAndLog`
- `ConsumeRollback`: 返回回滚状态（一次性消费）

**全部同步执行，零额外 LLM 调用，零额外延迟。**

### 2.4 Boot 装配

**文件**: `internal/dstsetup/dstsetup.go`

```go
func Init(a *agent.Agent, proofChain *core.ProofChain, workDir, compileCmd, testCmd string) *DSTRunner {
    hooks := dstvalid.NewDSTHooks(workDir)
    hooks.SetBuildCommands(compileCmd, testCmd)
    hooks.SetProofChain(proofChain)
    // Chain existing user hooks so DST and user hooks coexist.
    if existing := a.Hooks(); existing != nil {
        hooks.SetNext(existing)
    }
    a.SetHooks(hooks)
    return &DSTRunner{inner: a, hooks: hooks}
}
```

用户 hooks 和 DST hooks 通过 `SetNext` 链式调用——两者共存，互不干扰。

## 3. 验证分层

| 层级 | 检查内容 | 时间 | 在哪儿 | 失败做什么 |
|---|---|---|---|---|
| L0 | 编译/测试/文件快照 | 1-3s | PostToolUse（同步） | 回滚 + 修改 result |
| L1 | Proof Chain 状态摘要 | 0 | Compose（下回合注入） | Agent 自然读取已验证的状态 |

Proof Chain 在每个工具调用通过后记录：
```go
proofChain.Append("compile", "go build after edit_file", "OK")
```

Controller.Compose 把这些记录注入到下一回合的上下文中，Agent 能看到整个会话已累积验证了哪些东西。

## 4. 与设计提案的差异

下面这些是早期 DST v2 设计文档中提出的，但**在简化过程中被砍掉了**：

| 提案 | 状态 | 原因 |
|------|:--:|------|
| `VerifyHook` 接口 | ❌ 未实现 | 逻辑并入 `ConsumeRollback` — 编译测试失败直接修改 result |
| `TurnVerifyHook` 接口 | ❌ 未实现 | 回合结束 LLM 验证每步增加 5-10s 延迟，不可接受 |
| `Guard` 结构体 | ❌ 未实现 | 功能由 `DSTHooks` + `Snapshot` 完成 |
| L1-s 原子 LLM 验证 | ❌ 未实现 | 编译 + 测试已覆盖 80% 回归场景 |
| L2 回合结束聚合 LLM 验证 | ❌ 未实现 | 同上 — 保持零 LLM 延迟 |
| `RollbackReporter` 接口 | ❌ 已移除 | 2025-06 合并入 `ToolHooks.ConsumeRollback()` |

核心决策：**不引入异步 LLM 验证。** 编译 + 测试 + Proof Chain 已经让 Agent 在每回合有清晰的状态视野。

## 5. 实现文件清单（当前代码）

```
internal/agent/
├── agent.go          ← ToolHooks 接口（含 ConsumeRollback）
│                      + executeOne 中调用 ConsumeRollback

internal/dstvalid/
├── hooks.go          ← DSTHooks 实现 PreToolUse/PostToolUse/ConsumeRollback
├── validator.go      ← 编译/测试命令执行
├── snapshot.go       ← 文件快照 + 回滚

internal/dstsetup/
├── dstsetup.go       ← Init: 装配 DSTHooks → Agent，返回 DSTRunner facade
├── dstsetup_test.go  ← 10 个用例

internal/core/
├── atom.go           ← ProofChain: SHA-256 哈希链记录验证状态
```

总计：~400 行代码（`dstvalid` 约 324 行 + `dstsetup` 约 84 行 + `snapshot`）。无异步 goroutine，无 LLM 依赖。
