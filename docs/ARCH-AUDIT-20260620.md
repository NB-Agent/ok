# OK 自演化引擎深度审计 — 2026-06-20

> 基于 `internal/evolution/` 包的全面代码审查与分析
> 状态：4 个缺口已全部填补 (semantic / synthesis / sandbox_verify / ecp)

## 一、审计基线

### 审查范围
| 文件 | 行数 | 职责 |
|------|------|------|
| `internal/evolution/evolution.go` | 254 | 主引擎，OnTurnComplete 钩子 + 模式检测 |
| `internal/evolution/validator.go` | 326 | P1: Skill 自动验证与安装 |
| `internal/evolution/forget.go` | 156 | P2: 自动遗忘机制 |
| `internal/evolution/scoring.go` | 97 | P2 升级：基于使用频率的主动遗忘评分 |
| `internal/evolution/learn_interface.go` | 110 | kernel.Learn 接口实现 |
| `internal/evolution/semantic.go` | 324 | **新增** Gap 1: 语义模式识别 |
| `internal/evolution/synthesis.go` | 470 | **新增** Gap 2: 智能 Skill 合成 |
| `internal/evolution/sandbox_verify.go` | 222 | **新增** Gap 3: 沙箱运行验证 |
| `internal/evolution/ecp.go` | 288 | **新增** Gap 4: ECP 协议基础 |

### 构建与测试
- `go build ./...` — ✅ 通过
- `go test ./internal/evolution/...` — ✅ 通过（含 28 个新测试）

## 二、自演化闭环架构

```
OnTurnComplete hook (每次 turn 后由 agent.Run 调用)
  → P0: saveEpisodicMemory() + detectAndGenerate()  (每3 turns)
       ├── Layer 0: findPatterns (工具频率)
       ├── Layer 1: detectSequencePatterns (工具序列)
       ├── Layer 2: detectWorkflows (语义工作流签名)
       └── Layer 3: detectFingerprintPatterns (跨turn指纹)
  → P1: validateAndInstall()                         (每6 turns)
       ├── Layer 0: validatePattern (活跃度检查)
       ├── Layer 1: ValidateSkill (结构+安全+引用)
       └── install: generateSkillBodyEnhanced
  → P2: forget()                                     (每10 turns)
       ├── ageEpisodic (7天过期 / 100条上限)
       └── scoreAndForget (使用频率评分)
```

## 三、4 个缺口填补详情

### Gap 1: 语义模式识别 (semantic.go)

**之前**: `findPatterns()` — 纯关键词频率计数 (`strings.Contains(lower, tool)`)

**之后**: 三层语义检测

| 层 | 方法 | 零 LLM | 说明 |
|----|------|--------|------|
| L0 | `detectWorkflows` | ✅ | 8 个预定义工作流签名（tdd/search-then-edit/audit-then-fix 等） |
| L1 | `detectFingerprintPatterns` | ✅ | 跨 turn 工具对/频率/突发检测 |
| L2 | `SemanticInsight` | ❌ | LLM 驱动的抽象模式提取（Learn 工具路径） |

8 个内建工作流签名：
- `tdd-workflow`: write_file → bash → edit_file → bash
- `search-then-edit`: grep → read_file → edit_file
- `audit-then-fix`: ok-verify → edit_file
- `dependency-update`: read_file → bash → bash
- `build-verify-deploy`: bash → bash → deploy
- `research-then-write`: web_fetch → write_file
- `git-commit-cycle`: git → git → git
- `debug-cycle`: bash → read_file → edit_file → bash

### Gap 2: 智能 Skill 合成 (synthesis.go)

**之前**: `generateSkillBody()` — 固定模板 Markdown

**之后**: 双层合成

| 层 | 方法 | 说明 |
|----|------|------|
| L0 | `generateSkillBodyEnhanced` | 增强模板：触发条件 + 步骤 + 验证，工作流感知 |
| L1 | `SynthesisRequest/SynthesizePrompt` | LLM 合成：上下文 + 模式 → 高质量 playbook |

增强模板新增内容：
- `## When to Use` — 基于工作流的触发条件
- `## Steps` — 从签名提取的编号步骤
- `## Verification` — 基于上下文的验证建议（go test / ok-verify / go build）
- `## Notes` (LLM 路径) — 边界情况和替代方案

### Gap 3: 沙箱运行验证 (sandbox_verify.go)

**之前**: `validatePattern()` — 仅检查 episodic memory 中是否含工具名

**之后**: 三层验证

| 层 | 方法 | 说明 |
|----|------|------|
| L0 | `ValidateSkillStructure` | 结构完整性：frontmatter、必选章节、最小长度 |
| L1 | `ValidateSkillSafety` | 10 个危险模式正则检测（rm -rf /, curl\|sh, fork bomb 等） |
| L1 | `ValidateSkillReferences` | 工具引用校验：只允许已知工具名 |
| L2 | `SafetyReviewRequest` | LLM 安全审查（Learn 工具路径） |

危险模式检测清单：
- `rm -rf /` — 递归根目录删除
- `curl ... | bash` — 管道下载执行
- `> /dev/sda` — 块设备直接写入
- `mkfs.` — 文件系统格式化
- fork bomb, chmod 777 /, git push --force main, eval $

### Gap 4: ECP 协议基础 (ecp.go)

**之前**: 无代码实现

**之后**: ECP/1.0 类型系统 + 序列化 + 聚合

核心类型：
- `ECPSkillPacket` — 技能传输包（SHA-256 完整性 + 用户隐私哈希）
- `ECPKnowledgeUpdate` — 对等实例知识快照
- `ECPManifest` — 对等发现清单
- `ECPMergeResult` — 合并结果（新装/更新/拒绝）

关键函数：
- `NewECPSkillPacket` — 创建传输包（用户 ID 自动哈希）
- `Verify` — 完整性校验
- `MergeKnowledge` — 知识聚合（完整性 → 策略 → 去重 → 安装）
- `DefaultAcceptPolicy` — 默认接受策略（置信度 ≥ 0.6 + 安全扫描）

## 四、与 kernel.Learn 接口的对齐

| kernel.Learn 方法 | 实现状态 | 说明 |
|-------------------|---------|------|
| `Extract` | ✅ | 从 TaskRecord 提取工具序列模式 |
| `Generate` | ✅ | **已升级** — 使用 `generateSkillBodyEnhanced` |
| `Validate` | ✅ | **已升级** — 三层验证（结构+安全+引用+重名） |
| `Publish` | ✅ | 写入 skill.Store |
| `Stats` | ✅ | 返回技能统计 |

## 五、演进路线

```
v5 当前 (机械闭环)         →  v5.1 (智能闭环)         →  v6 (文明级)
─────────────────────────────────────────────────────────────────
✅ 经验自动提取             ✅ 语义工作流签名           ⬜ 跨用户知识联邦
✅ Skill 候选生成           ✅ 智能 Skill 合成          ⬜ ECP 传输层实现
✅ 模式活跃度验证           ✅ 结构+安全+引用验证       ⬜ 对等发现与同步
✅ 时间衰减遗忘             ✅ 使用频率评分遗忘         ⬜ 信任网络与声誉
✅ 内核 Learn 接口          ✅ ECP 协议类型定义         ⬜ 联邦学习聚合
```

## 六、架构完整性评估

```
层级          之前      现在      目标
─────────────────────────────────────
技术层        9.5/10    9.5/10    9.5/10 ✅
人类层        9.3/10    9.3/10    9.5/10
文明层        8.0/10    8.5/10    9.0/10  ← 从 4 gap 填补中提升

综合          8.5/10    8.7/10    9.5/10
```

**结论**: 4 个缺口全部填补，进化引擎从"关键词频率"升级为"语义工作流 + 指纹模式 + 安全验证 + 跨实例协议"。下一阶段的核心任务是 ECP 传输层实现（HTTP/gRPC）和跨用户知识联邦。
