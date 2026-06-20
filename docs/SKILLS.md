# Skill 编写指南

## 概述

Skill 是 OK 的**可调用 Playbook**——一个 Markdown 文件，包含 Agent 在特定任务中
应遵循的指令。两种运行模式：

- **inline**：skill 体作为工具结果注入当前对话（模型读取并立即应用）
- **subagent**：在隔离子 Agent 中执行，仅返回最终结论（tool 调用和推理不出现在父上下文中）

## 文件布局

Skill 可以放在以下任意目录（优先级升序，靠后的覆盖靠前的）：

```
项目根/.OK/skills/          ← 项目级（最高优先级）
项目根/.agents/skills/            ← 兼容 Claude Code
项目根/.agent/skills/
项目根/.claude/skills/

~/.OK/skills/               ← 用户全局
~/.agents/skills/
~/.agent/skills/
~/.claude/skills/
```

两种存放形式：

```
// 扁平文件
skills/review.md

// 目录形式（支持 references/ 引用目录）
skills/review/
  SKILL.md
  references/
    style.md
    patterns.md
```

## 格式

```markdown
---
name: review
description: Review pending changes — flags correctness / security / missing tests
runas: subagent
allowed-tools: read_file, grep, bash
---

# Review Skill

You are a code reviewer. Follow these steps:

1. Read the diff with `bash(git diff)`
2. For each changed file, read the full file with `read_file`
3. Check:
   - Logic errors
   - Missing error handling
   - Security issues
   - Missing tests
4. Output a structured review: file:line → issue → suggestion.
```

### Frontmatter 字段

| 字段 | 必须 | 说明 |
|------|:---:|------|
| `name` | 否 | Skill 标识符（默认用文件名）。`[a-zA-Z0-9._-]+`，1-64 字符 |
| `description` | **是** | 一行描述——显示在 Agent 的技能索引中 |
| `runas` | 否 | `inline`（默认）或 `subagent` |
| `allowed-tools` | 否 | 逗号分隔的工具名列表。仅 subagent 模式有效 |
| `model` | 否 | 指定使用哪个模型（如 `deepseek-pro`）。仅 subagent 模式有效 |

## Inline vs Subagent

| | Inline | Subagent |
|---|---|---|
| 上下文消耗 | 低（只注入一次 body） | 高（子 Agent 的所有 tool 调用在父侧不可见） |
| 隔离性 | 无（与父共享对话） | 完全隔离（独立 session） |
| 适用场景 | 简短指令/模板 | 多步探索/研究/审查 |
| 可用的工具 | 与父 Agent 相同 | 由 `allowed-tools` 限制 |

### inline 示例

```markdown
---
name: commit
description: Generate a concise commit message from the current diff
---

Generate a git commit message from the current diff. Format: `<type>: <summary>`.
Types: feat, fix, refactor, docs, test, chore.
Run `git diff --staged` first. When unstaged, run `git diff`. Keep under 72 chars.
```

### subagent 示例

```markdown
---
name: explore
description: Explore the codebase in an isolated subagent
runas: subagent
allowed-tools: read_file, grep, glob, ls, bash
---
[🧬 subagent]

You are an exploration subagent. The task you receive describes what to find.
Use read_file, grep, glob, and ls to search the codebase. Return a single distilled
answer with file:line citations. Do NOT make edits.
```

## 内置 Skill

以下 Skill 随 OK 内置，无需创建：

| Skill | 模式 | 说明 |
|-------|:---:|------|
| `explore` | subagent | 代码库只读探索，返回概要 |
| `research` | subagent | web_fetch + 代码阅读 |
| `review` | subagent | 审查当前 diff |
| `security_review` | subagent | 安全审查当前 diff |
| `test` | inline | 运行测试，诊断失败，修复 |

## 调用方式

- **模型自动调用**：通过 `run_skill` 工具
- **用户手动调用**：输入 `/<name>` 或 `/skill <name>`

```
> /review
[Agent 执行 review skill，输出审查结果]
```

## References 目录

目录形式的 skill 可以有一个 `references/` 子目录，包含额外的 `.md` 文件。
这些文件的内容会**追加到 skill body 后面**（按文件名排序），并在 skill 加载时
一起注入。这允许将大型 skill 拆分为多个文件。

```
skills/my-skill/
  SKILL.md            ← 主文件
  references/
    api.md             ← 会追加到 body
    constraints.md     ← 会追加到 body
```

## 测试 Skill

1. 将 `.md` 文件放入 `.OK/skills/` 目录
2. 启动 `ok chat`
3. 输入 `/<name>` 测试

`/skill paths` 显示所有搜索路径及其状态（ok/missing/not-directory/unreadable）。
`/skill list` 列出所有已加载的 skill。
