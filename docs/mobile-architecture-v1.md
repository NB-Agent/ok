# OK 移动端架构设计 v1

> 目标：手机 App（Flutter）通过 HCP 协议连接远程 OK Agent
> 约束：Agent 跑在 PC/服务器，App 是 thin client——不执行 bash/write_file/grep

---

## 一、核心架构

```
┌──────────────────────────────────────────────────────────────────────┐
│  移动端 (Flutter) — thin client                                        │
│                                                                      │
│  ┌──────────┐  ┌───────────┐  ┌────────────┐  ┌──────────────────┐ │
│  │ Chat UI  │  │ Connection│  │ Auth       │  │ Discovery        │ │
│  │ (消息气泡) │  │ Manager   │  │ (API Key / │  │ (手动输入 /      │ │
│  │ Markdown │  │ (服务器列表)│  │  OIDC)     │  │ 扫码 / mDNS)    │ │
│  │ 流式输出  │  │ 切换/持久化 │  │ Token 缓存 │  │ 本地保存         │ │
│  └──────────┘  └───────────┘  └────────────┘  └──────────────────┘ │
│                      │                                              │
│                  WSS (wss://)                                       │
│                  HCP 协议 (JSON-RPC over WebSocket)                 │
└──────────────────────┼──────────────────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────────────────────┐
│  OK Agent Server — 跑在 PC / 云服务器                                │
│                                                                      │
│  ok serve --host 0.0.0.0 --port 3030 [--tls-cert cert.pem --tls-key key.pem]
│                                                                      │
│  路由:                                                               │
│  ├── GET /healthz       → 健康检查 (无需认证)                        │
│  ├── GET /acp           → 协议信息                                   │
│  ├── GET /ws            → HCP WebSocket (带认证)                     │
│  ├── POST /api/register → 注册 API Key (首次)                        │
│  ├── GET /events        → SSE (已有)                                 │
│  └── POST /submit 等    → REST 命令 (已有)                           │
│                                                                      │
│  中间件链:                                                            │
│  ┌─ TLS (wss://) ──→ Auth (API Key / OIDC) ──→ WebSocket Handler ─┐│
│  └─────────────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────────────┘
```

---

## 二、三阶段演进

### Phase 1：局域网直连 (现在就能做)

```
手机 ──WSS──→ PC (同一局域网)
              │
              ok serve --host 0.0.0.0 --port 3030 --api-key "xxx"

安全性：WSS + API Key
发现：  手动输入 IP:端口 + Key / 扫码二维码
```

### Phase 2：公网连接 (有公网 IP / DDNS)

```
手机 ──WSS──→ 云服务器 / 家里有公网 IP 的 PC
              │
              ok serve --host 0.0.0.0 --port 443 --tls-cert --tls-key
              Nginx 反代 (可选)
              
安全性：WSS + API Key + OIDC
发现：  域名 + Key
```

### Phase 3：中继模式 (无公网 IP)

```
手机 ←──WSS──→  中继服务器 (TURN/WebRTC)  ←──WSS──→  PC 上的 Agent
                 │
                 ok-relay (新增组件)
                 只转发加密数据流，不存储

安全性：端到端加密 (E2EE)，中继不可读
发现：  中继服务器地址 + 配对码
```

---

## 三、服务端改动 (Go)

### 3.1 `ok serve` 新增参数

```go
--host string      监听地址 (默认 "127.0.0.1"，改成 "0.0.0.0" 允许外部访问)
--port int         端口 (默认 3030)
--tls-cert string  TLS 证书路径 (提供则启用 WSS)
--tls-key string   TLS 密钥路径
--api-key string   简单认证密钥 (未设置则只允许本地连接)
```

### 3.2 新增：认证中间件

在 `internal/serve/auth.go`：

```go
// 认证策略：
// 1. 无 --api-key → 只允许本地连接 (127.0.0.1 / ::1)
// 2. 有 --api-key → HTTP Header `Authorization: Bearer <key>`
// 3. 有 --sso      → OIDC 验证 (已有代码)
```

### 3.3 新增：WebSocket 认证

HCP `/ws` 升级前验证 token：

```go
// WebSocket 升级时验证 token
// 方法1: URL query param: wss://host:port/ws?token=xxx
// 方法2: 先 POST /auth 获取 token，再连接 ws
```

### 3.4 不变的部分

- HCP 协议 (`hcp_ws.go`) — 命令/事件格式不变
- Broadcaster (`broadcaster.go`) — 事件分发不变
- Controller 层 — 完全不变

---

## 四、Flutter 端改动

### 4.1 新增：Connection Manager

```
lib/services/connection_manager.dart
│
├── ServerProfile
│   ├── id: String          (UUID)
│   ├── name: String        (用户命名的别名)
│   ├── host: String        (IP / 域名)
│   ├── port: int           (默认 3030)
│   ├── useTls: bool        (wss:// vs ws://)
│   ├── apiKey: String      (认证密钥)
│   └── createdAt: DateTime
│
├── 持久化: SharedPreferences / SQLite
├── 增删改查: add / remove / update / list
└── 自动连接: 启动时连接上次使用的服务器
```

### 4.2 新增：服务器列表/选择界面

```
lib/screens/server_list_screen.dart
│
├── 已保存的服务器列表 (滑动删除)
├── "添加服务器" 按钮
├── 手动输入表单 (名称 / IP / 端口 / Key)
├── 扫码添加 (二维码编码 ServerProfile JSON)
└── 连接状态指示器 (绿色/红色)
```

### 4.3 修改：HcpClient

```dart
// 当前: 固定 ws://127.0.0.1:3030/ws
// 改为: 从 ConnectionManager 读取当前 ServerProfile

HcpClient.fromProfile(ServerProfile profile) {
  final scheme = profile.useTls ? 'wss' : 'ws';
  final url = '$scheme://${profile.host}:${profile.port}/ws';
  // 认证: ws URL 带 token 参数
  if (profile.apiKey.isNotEmpty) {
    url += '?token=${profile.apiKey}';
  }
}
```

### 4.4 新增：设置页面合并

当前有 Settings panel（切换服务器 URL），改为完整的"服务器管理"页面 + 设置页面。

### 4.5 不变的部分

- `models/event.dart` — 事件模型完全不变
- `services/session_manager.dart` — 状态管理基本不变
- `screens/chat_screen.dart` — 聊天界面基本不变

---

## 五、手机扫码配对流程

```
PC 端 (浏览器 / 桌面 App):
┌──────────────────────────────────┐
│  OK Agent Web UI (serve/index)   │
│                                  │
│  ┌────────────────────────────┐  │
│  │  📱 手机连接              │  │
│  │                            │  │
│  │  服务器地址: 192.168.1.5   │  │
│  │  API Key:    ok_a3b2c1... │  │
│  │                            │  │
│  │  [📲 显示二维码]           │  │
│  └────────────────────────────┘  │
└──────────────────────────────────┘
         ↓ 手机扫码
┌──────────────────────────────────┐
│  手机 App                        │
│  ├── 解析二维码 → ServerProfile  │
│  ├── 保存到 ConnectionManager   │
│  ├── 自动连接                    │
│  └── 进入聊天界面                │
└──────────────────────────────────┘
```

二维码内容 (JSON 压缩):
```json
{
  "v": 1,
  "h": "192.168.1.5",
  "p": 3030,
  "k": "ok_a3b2c1d4e5",
  "t": false
}
```

---

## 六、认证流程

```
Flutter App                  OK Server
    │                            │
    │  WSS 连接 + ?token=xxx     │
    │───────────────────────────→│
    │                            │── 验证 token
    │                            │── 记录 session
    │  101 Switching Protocols   │
    │←───────────────────────────│
    │                            │
    │  {"type":"submit",...}     │
    │───────────────────────────→│
    │  {"kind":"text",...}       │
    │←───────────────────────────│
```

API Key 生成:
```go
// ok serve --api-key generate
// 输出: ok_a3b2c1d4e5f6...

// 验证:
func validateAPIKey(r *http.Request, expectedKey string) bool {
    // 1. Header: Authorization: Bearer ok_xxx
    // 2. Query:  ?token=ok_xxx
    // 3. 恒定时间比较，防时序攻击
}
```

---

## 七、文件结构变化

### Go 端新增

```
internal/serve/
├── serve.go        ← 修改: 支持 --host / --tls / --api-key 参数
├── auth.go         ← 新增: API Key 认证中间件
├── hcp_ws.go       ← 修改: WebSocket 升级前验证 token
├── serve_test.go   ← 新增: 认证测试
└── ...
```

### Flutter 端重构

```
sdk/flutter/lib/
├── main.dart                           ← 修改: 改为服务器列表首页
├── models/
│   ├── event.dart                      ← 不变
│   └── server_profile.dart             ← 新增: 服务器配置模型
├── screens/
│   ├── chat_screen.dart                ← 小改: 接收 ServerProfile
│   ├── server_list_screen.dart         ← 新增: 服务器列表页
│   └── server_add_screen.dart          ← 新增: 添加服务器页
├── services/
│   ├── hcp_client.dart                 ← 修改: 支持 WSS + 认证
│   ├── session_manager.dart            ← 小改
│   └── connection_manager.dart         ← 新增: 服务器配置管理
└── widgets/
    ├── message_bubble.dart             ← 从 chat_screen 提取
    └── server_card.dart                ← 新增: 服务器卡片组件
```

---

## 八、不做的事情 (设计边界)

| 不该做的事 | 理由 |
|:----------|:------|
| ❌ 手机端执行 bash/write_file/grep | OS 权限限制，Agent 跑在远端 |
| ❌ 手机端跑 LLM 模型 | 本地小模型 (Ollama) 支持留到 Phase 3 |
| ❌ P2P 直接连接 | 需要 NAT 穿透，复杂度太高 |
| ❌ 消息端到端加密 | Phase 2 再考虑，先走 WSS |
| ❌ 离线消息队列 | 移动端网络不稳定，但先做基本连接 |
| ❌ 推送通知 | 需要 Firebase/APNs，Phase 2 |
| ❌ 文件上传/图片 | Phase 2 通过 HCP 扩展消息类型 |

---

## 九、实施顺序

```
Step 1: ok serve 支持 --host 0.0.0.0          (30分钟)
Step 2: 新增 auth.go API Key 中间件             (45分钟)
Step 3: WebSocket 认证接入                      (30分钟)
Step 4: Flutter ConnectionManager              (45分钟)
Step 5: Flutter 服务器列表界面                    (60分钟)
Step 6: HcpClient 接入 WSS + 认证               (30分钟)
Step 7: 扫码配对（Web UI 显示二维码）              (60分钟)
Step 8: 测试 + 构建                              (30分钟)
                                              ─────────
                                              约 5 小时
```

---

## 十、关键决策记录

1. **用 API Key 而不是 OIDC 作为默认认证**
   - 理由：零配置，手机 App 只需要输入一串字符
   - OIDC 留给企业用户

2. **WebSocket 用 URL query param 传 token**
   - 理由：WebSocket 标准 upgrade 流程不支持自定义 header
   - 替代方案：先 REST 登录拿 JWT，但增加一次往返

3. **局域网优先，不依赖云服务**
   - 理由：用户自己控制数据，不经过第三方
   - 云服务是 Phase 2 的事

4. **服务器配置存在手机本地**
   - SharedPreferences 足够，不需要 SQLite
   - 用户换手机时可通过扫码迁移

5. **不引入 WebRTC**
   - WebRTC 需要 STUN/TURN 服务器，架构复杂度翻倍
   - 公网直连走 WSS（有公网 IP 或 Cloudflare Tunnel）
   - 无公网 IP → Cloudflare Tunnel / frp（外部工具，不是 OK 的职责）
