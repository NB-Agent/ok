# OK Agent — Flutter 移动端 App

OK Agent 的移动端入口，通过 HCP WebSocket 协议连接到任意 OK Agent 实例。

## 功能

- 💬 **聊天界面** — 发送消息，Markdown 渲染，流式输出
- 🔌 **HCP WebSocket** — 通过 `ws://host:port/ws` 双向通信
- 🎨 **Material 3** — 浅色/深色主题自动切换
- ⚡ **Token 用量显示** — 每次对话的 token 和费用
- 🔧 **工具调用可视化** — agent 调用工具时实时显示
- 📱 **iOS + Android** — 一套代码双平台

## 快速开始

```bash
# 1. 确保 OK Agent 正在运行
ok serve

# 2. 克隆并运行 App
cd sdk/flutter
flutter pub get
flutter run
```

## 架构

```
lib/
├── main.dart                         # 入口 + 主题配置
├── models/
│   └── event.dart                    # 事件模型（匹配 HCP 协议）
├── screens/
│   └── chat_screen.dart              # 主聊天界面
├── services/
│   ├── hcp_client.dart               # HCP WebSocket 客户端
│   └── session_manager.dart          # 会话管理 + 状态
└── widgets/                          # (待扩展)
```

## 依赖

| 包 | 用途 |
|:---|:------|
| `web_socket_channel` | WebSocket 连接 |
| `provider` | 状态管理 |
| `flutter_markdown` | Markdown 渲染 |
| `shared_preferences` | 本地持久化 |

## HCP 协议

客户端通过 WebSocket 连接到 OK Agent 的 `/ws` 端点：

**Client → Server (JSON 命令)**
```json
{"type":"submit", "input":"你好"}
{"type":"cancel"}
{"type":"new_session"}
```

**Server → Client (JSON 事件)**
```json
{"kind":"text","text":"你好！"}
{"kind":"tool_dispatch","tool":{"name":"bash","args":"..."}}
{"kind":"done"}
```

详细协议见 `internal/serve/hcp_ws.go`。
