# ok-verify — OK 项目全量静态分析器插件

## 设计原则
- 注册为 `go vet` 插件，每次 `go build` 前自动运行
- 只做**确定性检查**（AST/SSA 模式匹配），不做概率推理
- 每个检查器独立文件，可单独启用/禁用
- 零误报目标：宁可漏报也不误报

## 检查器清单

### 🔴 严重（可能导致崩溃/数据丢失）

| # | 检查器 | 检测内容 | 检测方法 | 优先级 |
|---|--------|---------|---------|--------|
| 1 | `recovercheck` | goroutine 没有 defer-recover | AST: go func() 体内无 recover | P0 |
| 2 | `closecheck` | defer Close/CloseWithError 不检查错误 | AST: defer sel.Close() | P0 |
| 3 | `rawassert` | 类型断言没有 comma-ok | AST: x.(T) 不在赋值语句中 | P0 |
| 4 | `nilmapwrite` | 可能为 nil 的 map 写入 | AST: map literal 赋值后可能被置 nil | P1 |
| 5 | `closereadcheck` | 只读文件用 defer Close() 但未检查错误 | AST: os.Open → defer Close() | P1 |
| 6 | `doubleclose` | channel 可能被两次 close | AST: 多个 close(ch) 无 sync.Once | P1 |
| 7 | `tmpdircheck` | MkdirTemp 没有 defer RemoveAll | AST: 创建临时目录后无清理 | P1 |

### 🟡 中等（逻辑缺陷）

| # | 检查器 | 检测内容 | 检测方法 |
|---|--------|---------|---------|
| 8 | `switchdefault` | switch 没有 default 分支 | AST: body 无 default case |
| 9 | `nilerr` | err != nil 返回 nil, nil | AST/SSA |
| 10 | `bodyclose` | HTTP 响应体未关闭（补全现有遗漏） | AST: 所有 resp.Body 路径有 Close |
| 11 | `mutexcopy` | 结构体值传递包含 sync.Mutex | AST: 值参数含 sync.Mutex 字段 |
| 12 | `shadow` | 变量遮蔽 | AST: 内层作用域重复声明同名变量 |
| 13 | `deferinloop` | for 循环内 defer | AST: defer 在 for 体内 |
| 14 | `loopclosure` | goroutine 闭合循环变量 | AST: go func 引用 for 变量 |
| 15 | `sprintfhex` | fmt.Sprintf("%x") 应改用 hex | AST: %x 格式化 fmt.Sprintf |
| 16 | `sleepinloop` | time.Sleep 在 for 内（应为 timer） | AST: for 体内 time.Sleep |

### 🟢 低（性能/风格）

| # | 检查器 | 检测内容 | 检测方法 |
|---|--------|---------|---------|
| 17 | `stringcastloop` | []byte(string) 在循环中重复转换 | AST: for 体内显式转换 |
| 18 | `contextbg` | 不应使用 context.Background() 的场景 | AST: 特定包中调用 Background |
| 19 | `preallocslice` | 可预分配未预分配的 append | SSA: make([]T,0) 无 cap |
| 20 | `latecancel` | defer cancel() 距离 context.WithCancel 过远 | AST: WithCancel 和 defer 间有阻塞操作 |

## 技术实现

```go
// internal/verification/analyzer.go
package verification

import (
    "golang.org/x/tools/go/analysis"
    "golang.org/x/tools/go/analysis/multichecker"
)

func main() {
    multichecker.Main(
        RecoverAnalyzer,
        CloseCheckAnalyzer,
        RawAssertAnalyzer,
        SwitchDefaultAnalyzer,
        NilMapWriteAnalyzer,
        DoubleCloseAnalyzer,
        TmpDirCheckAnalyzer,
        NilErrReturnAnalyzer,
        BodyCloseAnalyzer,
        MutexCopyAnalyzer,
        SprintfHexAnalyzer,
        DeferInLoopAnalyzer,
        LoopClosureAnalyzer,
        ContextBgAnalyzer,
        PreallocSliceAnalyzer,
        StringCastLoopAnalyzer,
        LateCancelAnalyzer,
    )
}
```

## 使用方法

```bash
# 安装
cd internal/verification && go build -o ok-verify

# 在项目中运行（每次提交前）
cd /path/to/ok && go vet -vettool=internal/verification/ok-verify ./...

# 在 CI 中
- run: go vet -vettool=internal/verification/ok-verify ./...
```

## 预期效果

| 指标 | 当前（LLM 采样） | 改进后（全量分析） |
|------|----------------|------------------|
| 覆盖率 | ~15% | **100%** |
| 检查耗时 | 5-10 分钟 | **< 1 秒** |
| 每次成本 | 数百万 tokens | **0 tokens** |
| 发现数量/次 | 5-8 个 | **~200+ 个** |
| 可重复性 | 概率性（每次不同） | **完全确定性** |
