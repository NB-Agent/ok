---
key: episodic-21
type: episodic
created: 2026-06-21T09:10:14Z
source: auto-evolution
---

## Input
[Task Tree]
✅ (root)
✅ (root)
✅ (root)
✅ (root)
✅ (root)
✅ (root)
✅ (root)
✅ (root)
✅ (root)
✅ Analyze token consumption root causes-1


# Environment

# Self state
- Git: master 📝dirty
- Built-in ...

## Output
结合子 agent 分析和我的代码审计，给你一个明确结论：

---

## 🎯 准确说：共享知识是 **前缀膨胀的主因**，但不是 token 加速的**唯一原因**

```
你的 token 消耗拆解：

┌──────────────────────────────────────────────────────────┐
│                     每次 Session 启动                      │
├──────────────────────────────────────────────────────────┤
│ ① 基础系统提示词              ~3,000 tokens    ████████   │
│ ② 项目记忆 (158→截断30条)    ~900 tokens      ██         │
│ ③ 共享知识 (136条，没截断)   ~4,000 tokens    ██████████ ← 最大单块 ❌│
│ ④ 技能索引 (26个)             ~500 tokens   ...
