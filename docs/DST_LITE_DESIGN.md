# DST-Lite：最大化效率方案

## 核心结论：外部裁判无用

同一个 LLM 评判自己的工作 = 浪费 token 给自己发提醒。真正有用的验证必须满足三个条件之一：

1. **确定性**（不依赖 LLM 判断）→ 编译、测试、解析器
2. **自验**（Agent 自己在工作流中验证）→ 写完读回，与预期对比
3. **外部独立**（不同模型、不同 session）→ 暂不可行（成本/延迟）

当前 DST 三者都不满足。DST-Lite 只保留 1+2，删除 3。

## 三层验证，零额外 LLM 调用

```
┌──────────────────────────────────────────────────────┐
│ L0 · 结构完整性检查（确定性，每步 <0.5s，0 token）        │
│   write_file 后自动:                                  │
│   ✓ 文件非空？                                        │
│   ✓ Go 文件 → gofmt -e 能解析？                       │
│   ✓ JSON 文件 → encoding/json 能解析？                │
│   ✓ 其他 → 至少第一行非空                             │
│   ✗ → 结果注入: "[L0 FAIL] file is empty/syntax error"│
└──────────────────────────────────────────────────────┘
┌──────────────────────────────────────────────────────┐
│ L1 · 系统提示词自验（0 额外调用，~50 token 在 prompt 里） │
│   在 system prompt 中强化:                             │
│   "写完文件立即读回确认内容。todo 项必须验证后才标记完成。 │
│    不要信任 write_file 的成功返回值——读回文件检查。"     │
└──────────────────────────────────────────────────────┘
┌──────────────────────────────────────────────────────┐
│ L2 · 回合结束自审（~200 token/回合，无独立 LLM）        │
│   回合结束时，往 session 注入一条 user 消息:            │
│   "你本回合修改了 files X,Y,Z。请确认每个文件内容正确。  │
│    如有问题立即修正。没问题回复 'confirmed'。"           │
│   这是 Agent 自己的 LLM 读自己的输出，不是独立裁判。      │
│   成本：一条消息。效果：强迫 Agent 回头看自己写了什么。   │
└──────────────────────────────────────────────────────┘
```

## L0 实现（agent/agent.go 内 20 行）

```go
// executeOne 中，PostToolUse 之后：

if isWriter(name) && err == nil {
    if l0 := structuralCheck(name, json.RawMessage(args), result); l0 != "" {
        result += "\n\n" + l0
    }
}

func structuralCheck(name string, args json.RawMessage, result string) string {
    path := extractPathFromArgs(name, args)
    fi, statErr := os.Stat(path)
    if statErr != nil {
        return "[L0 WARN] file not found after write — write may have failed"
    }
    if fi.Size() == 0 {
        return "[L0 FAIL] file is 0 bytes — content was not written"
    }
    // 只有 Go/JSON 文件做解析验证（已内置在标准库，零依赖）
    if strings.HasSuffix(path, ".go") {
        if out, err := exec.Command("gofmt", "-e", path).CombinedOutput(); err != nil {
            return fmt.Sprintf("[L0 FAIL] gofmt parse error:\n%s", string(out))
        }
    }
    if strings.HasSuffix(path, ".json") {
        data, _ := os.ReadFile(path)
        if !json.Valid(data) {
            return "[L0 FAIL] invalid JSON — file may be truncated or malformed"
        }
    }
    return "" // pass
}
```

**为什么有用：** 最常见的 Agent 失败模式——写了空文件、截断文件、语法错——在 0.3 秒内被拦住。Agent 下一步就看到 `[L0 FAIL]`，自然修正。

## L1 实现（修改系统提示词）

当前 system prompt 中有"倒三角拆解—验证"的描述，增加一行：

```
写完文件后必须立即用 read_file 读回文件，确认内容与你意图一致后才能继续。
不要信任 write_file 的返回值——那是文件系统的应答，不是你意图的验证。
```

改 system prompt 和改代码的区别：改 prompt 不需要编译。Agent 看到这条规则就会照做。

## L2 实现（agent/agent.go Run() 内 15 行）

```go
// 在 executeBatch 返回后、session.Add tool results 后、maybeCompact 前：

if a.selfAudit && hasWriter(calls) {
    files := writtenFiles(calls, results)
    if len(files) > 0 {
        a.session.Add(provider.Message{
            Role:    provider.RoleUser,
            Content: fmt.Sprintf(
                "[self-audit] You just modified: %s\n"+
                "Read back each file and confirm the content is correct.\n"+
                "Fix any issues immediately. Reply 'confirmed' when done.",
                strings.Join(files, ", ")),
        })
        // 多给一步让 Agent 读回确认
        selfAuditStep := a.maxSteps
        a.maxSteps = step + 2  // 额外 2 步用于读回+确认
        // ... 这需要一些重构，细节略 ...
    }
}
```

**为什么有用：** Agent 在长任务中倾向于"写了就忘"。注入这条消息强迫它回头看。人类 code review 也做同样的事——不是检查语法，而是"你再读一遍你写的"。

## 与当前版本对比

| | 当前 DST | DST-Lite |
|---|---|---|
| 额外 LLM 调用 | 每步 0-5 次（异步） | **0** |
| 额外 token | ~2000/回合 | **~200/回合**（L2 消息） |
| 延迟增加 | 0（异步但不生效） | **~0.3s/写入**（L0） |
| 失败强制力 | advisory | **tool result 内联 + Agent 自读** |
| 空文件/截断检测 | 无 | **L0 捕获** |
| 语法错误检测 | 编译时才报 | **L0 立报** |
| 意图验证 | PCVA/Game/Judge | **L1 提示词自验** |
| bash 白名单 | ❌ 降低能力 | **删除**（L0+L1 保证安全） |
| 代码量 | ~800 行 | **~150 行新 + 删 ~200 行旧** |

## 删除清单

```
internal/dstvalid/hooks.go:
  - asyncDSTVerify()          (~50行)
  - runDSTVerification()      (~100行)
  - atomsRelatedToFile()      (~30行)
  - readRelevantCode()        (~40行)
  - truncateCode()            (~12行)
  - asyncSem 字段              (1行)
  - safeBashPrefixes + isSafeBash (~60行)

internal/dstvalid/validator.go:  (~200行，完整删除或归档)

internal/game/game.go:           (~370行，删除)
internal/judge/judge.go:         (~430行，删除)
internal/core/atom.go:           保留（原子类型定义仍有价值）
internal/core/pcva.go:           保留（类型定义）
internal/atomizer/atomizer.go:   保留（原子提取 + Verification推断）
internal/cycle/cycle.go:         保留（PCVA 回环可用于 /dst check 命令）
```

## 保留的内部包

| 包 | 保留原因 |
|---|---|
| `core/` | Atom/AtomSet/ProofChain 类型定义，L2 可能引用 |
| `atomizer/` | 从 LLM 输出提取原子，inferVerification 有价值 |
| `cycle/` | PCVA 回环引擎，可保留给手动 `/dst check` 命令 |
| `dstvalid/hooks.go` | L0 编译/测试检查（PostToolUse 中的同步部分） |
| `dstsetup/` | 改为注入 Guard 而非完整 DST |

## 实现计划

| Phase | 内容 | 行数 |
|---|---|---|
| 1 | L0 structuralCheck + executeOne 插入点 | +25 |
| 2 | L2 self-audit 消息注入 | +20 |
| 3 | 修改系统提示词（L1） | +3 |
| 4 | 删除 async DST 代码 | -400 |
| 5 | 删除 game/judge 包 | -800 |
| 6 | 清理 dstsetup → 简化 Init | -50 |
| 7 | 删除 bash 白名单 | -60 |
| 8 | 测试 | +50 |

净变化：**-1200 行代码，+3 行 prompt**。0 额外 LLM 调用。

## 为什么这是最优方案

1. **最小成本** — 不花 token 做验证，把 token 留给真正的工作
2. **最高可靠性** — L0 是确定性的，不会误判
3. **自然闭合** — Agent 读回自己写的文件 = 天然的验证闭环
4. **通用性** — 不依赖 Go/特定语言。L0 可扩展任意格式的解析器
5. **零延迟** — L0 在 0.1-0.5s 内完成，不阻塞 Agent
6. **不降低能力** — 删除 bash 白名单，Agent 恢复完整操作能力

**一句话：不替 Agent 思考——让它自己验证自己。**
