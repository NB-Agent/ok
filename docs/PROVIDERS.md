# Provider 扩展开发指南

## 概述

在 OK 中添加新模型后端非常简单。Provider 层使用工厂注册模式——
实现 `provider.Provider` 接口，`init()` 中注册，用户就能在 `OK.toml` 中
通过 `kind = "your-kind"` 使用。

## 最小实现

```go
// internal/provider/anthropic/anthropic.go
package anthropic

import (
    "context"

    "OK/internal/provider"
)

func init() {
    provider.Register("anthropic", New)
}

func New(cfg provider.Config) (provider.Provider, error) {
    if cfg.BaseURL == "" {
        return nil, fmt.Errorf("anthropic: base_url is required")
    }
    return &client{
        name:    cfg.Name,
        apiKey:  cfg.APIKey,
        baseURL: strings.TrimRight(cfg.BaseURL, "/"),
        model:   cfg.Model,
    }, nil
}
```

## Provider 接口

```go
type Provider interface {
    Name() string
    Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}
```

### Name()

返回实例名（如 `"deepseek"`），用于日志和状态栏显示。

### Stream(ctx, req) → chan Chunk

发送 `Request`，返回一个流式 `Chunk` channel。关键要求：

1. **ctx 取消必须终止底层 HTTP 请求** — `http.NewRequestWithContext(ctx, ...)`
2. **channel 关闭表示结束** — 即使出错也要先发送 `ChunkError` 再 close(ch)
3. **Chunk 类型必须按协议顺序**：

```
Reasoning deltas → Text deltas → ToolCallStart → ToolCall(s) → Usage → close
```

### Chunk 类型

| ChunkType | 何时发送 | 字段 |
|-----------|---------|------|
| `ChunkReasoning` | thinking 模式的推理文本 | `Text` |
| `ChunkText` | 可见回答文本 | `Text` |
| `ChunkToolCallStart` | 工具调用开始（ID+Name 已确定） | `ToolCall.ID`, `ToolCall.Name` |
| `ChunkToolCall` | 一个完整工具调用 | `ToolCall.*` |
| `ChunkUsage` | token 统计 | `Usage.*` |
| `ChunkError` | 错误 | `Err` |

### Usage 字段

Provider 需要填充 `Usage.PromptTokens`、`CompletionTokens`、`TotalTokens`。
可选填 `CacheHitTokens`/`CacheMissTokens`（DeepSeek 前缀缓存统计）。

## Config 结构

```go
type Config struct {
    Name      string         // 实例名 ("my-provider")
    BaseURL   string         // API 端点
    Model     string         // 模型 ID
    APIKey    string         // 已解析的 API key
    APIKeyEnv string         // key 来源的环境变量名（用于错误提示）
    Extra     map[string]any // kind 专属配置
}
```

`APIKeyEnv` 用于生成用户可操作的认证错误信息：

```go
func (e *AuthError) Error() string {
    key := "the API key"
    if e.KeyEnv != "" {
        key = e.KeyEnv
    }
    return fmt.Sprintf("authentication failed for provider %q (HTTP %d): %s is invalid or expired",
        e.Provider, e.Status, key)
}
```

## AuthError — 认证失败

当 API 返回 401/403 时，返回 `provider.AuthError`（而非通用 error）：

```go
if resp.StatusCode == http.StatusUnauthorized {
    return nil, &provider.AuthError{
        Provider: cfg.Name,
        KeyEnv:   cfg.APIKeyEnv,
        Status:   resp.StatusCode,
    }
}
```

这样 CLI 会显示精确的错误信息，指导用户更新 `.env` 文件。

## 流式 retry

Agent 重试 `streamOnce` 最多 2 次（指数退避）。只对 transient 错误重试（502/503/504/connection reset）。
Permanent 错误（认证、坏请求）立即失败。

Provider 不需要自己实现重试。

## 示例：OpenAI 兼容 Provider

```go
func (c *client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
    ch := make(chan provider.Chunk)

    body := map[string]any{
        "model":       c.model,
        "messages":    req.Messages,
        "stream":      true,
        "temperature": req.Temperature,
    }
    jsonBody, _ := json.Marshal(body)

    httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
    if err != nil {
        return nil, err
    }
    httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Accept", "text/event-stream")

    go func() {
        defer close(ch)
        resp, err := c.http.Do(httpReq)
        if err != nil {
            ch <- provider.Chunk{Type: provider.ChunkError, Err: err}
            return
        }
        defer resp.Body.Close()

        if resp.StatusCode == 401 || resp.StatusCode == 403 {
            ch <- provider.Chunk{Type: provider.ChunkError,
                Err: &provider.AuthError{Provider: c.name, KeyEnv: c.keyEnv, Status: resp.StatusCode}}
            return
        }
        if resp.StatusCode != 200 {
            ch <- provider.Chunk{Type: provider.ChunkError,
                Err: fmt.Errorf("%s: HTTP %d", c.name, resp.StatusCode)}
            return
        }

        scanner := bufio.NewScanner(resp.Body)
        for scanner.Scan() {
            // Parse SSE "data: {...}" frames → ChunkReasoning/ChunkText/ChunkUsage
        }
    }()
    return ch, nil
}
```

## 测试

`internal/provider/openai/openai_test.go` 是一个完整的参考实现。
Provider 包本身的注册/New/Kinds 有基础单元测试。
