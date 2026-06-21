---
key: episodic-9
type: episodic
created: 2026-06-21T12:30:59Z
source: auto-evolution
---

## Input
很明显现在的费用大头就是这样产生的几十万几十万的未命中

## Output
全部通过。

---

## 总结：缓存不会因为你担心的原因失效

### OK 做了三件事保缓存

| 机制 | 之前 | 现在 |
|------|------|------|
| Memory 快照 | Boot 时读进 `Set.Index`，会话中不变 | ✅ 没变 |
| Compact 时 Messages[0] | 系统消息原样保留 | ✅ 没变 |
| **tool-groups 切换** | 即时改 registry → 下轮请求体变了 → 💥全 miss | ✅ **延迟到turn边界** |

### 唯一会触发的 miss（无法避免也不需要避免）

| 场景 | 频率 | 影响 |
|------|:---:|------|
| 跨会话间保存了新的 `remember` | 偶尔 | 1次冷启动，下轮即热 |
| Compact 裁剪后下一轮 | 极少 | 1次冷启动 |
| tool-groups 切换后的下一轮 | 偶尔 | **现在也是1次冷启动（修前是每轮miss）** |

### 修前的坑 vs 修后

```
修前: Turn 10 too...
