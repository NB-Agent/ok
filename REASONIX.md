# OK 多 Agent 编排协议

OK 内置了一套多 Agent 推理编排框架。一个复杂任务被递归分解为子任务，
分派给专门的 agent 执行，结果聚合后返回。

> 文件名 `REASONIX.md` 是历史遗留——这是 OK 自身的协议，不是外部项目。
> 新项目推荐使用 `AGENTS.md` 或 `OK.md`。

## 核心概念

- **Reasoner** — 主推理 agent。接收用户任务，分解为子任务，分派给 Specialists，
  聚合结果后返回最终答案。
- **Specialist** — 领域专家 agent。执行特定类型的子任务（代码、搜索、桌面操控等）。
- **Task** — 一个可执行的原子工作单元，包含目标、上下文和结果。
- **Plan** — Task 的有向无环图（DAG），表示分解后的任务依赖关系。

## 接口

```go
// Reasoner decomposes a goal into sub-tasks and dispatches them.
type Reasoner interface {
    // Reason breaks down a goal and returns the final result.
    Reason(ctx context.Context, goal string) (string, error)
}

// Specialist handles one domain of sub-tasks.
type Specialist interface {
    Name() string
    Description() string
    CanHandle(task Task) bool
    Execute(ctx context.Context, task Task) (TaskResult, error)
}

// Task is a unit of work for a specialist.
type Task struct {
    ID        string
    Goal      string
    Context   string   // relevant context from parent task
    DependsOn []string // task IDs that must complete first
}

// TaskResult is the output of a completed task.
type TaskResult struct {
    TaskID  string
    Summary string
    Output  string
    Error   string
}
```

## 工作流

1. **Decompose** — Reasoner 将用户目标分解为 Task DAG
2. **Dispatch** — 就绪的 Task（所有依赖已完成）分派给能处理它的 Specialist
3. **Execute** — Specialist 执行 Task，产生 TaskResult
4. **Aggregate** — 所有 Task 完成后，Reasoner 聚合结果生成最终答案

## 内置 Specialists

| Name | Description | 能力 |
|------|------------|------|
| `orchestrator` | 主 Reasoner，分解+聚合 | 通用推理 |
| `researcher` | 搜索调研 | web_fetch, browser |
| `coder` | 编程实现 | read/write, edit, go build |
| `desktop` | 桌面操控 | computer-use, screenshot |
| `analyst` | 数据分析 | bash, database |

## 用法

```go
// In agent/team.go or wherever the team is assembled:
specialists := []agent.Specialist{
    {Name: "researcher", Description: "Web research", Prompt: researchPrompt, ...},
    {Name: "coder", Description: "Code writing", Prompt: codePrompt, ...},
}
reasoner := agent.NewReasoner(orchestratorRunner, specialists, sink)
result, err := reasoner.Reason(ctx, "build a web app that searches GitHub")
```
