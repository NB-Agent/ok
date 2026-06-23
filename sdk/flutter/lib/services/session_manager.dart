/// Session manager — tracks conversation history and client state.
library;

import 'package:flutter/foundation.dart';

import '../models/event.dart';
import 'hcp_client.dart';

/// One message in the chat display.
class ChatMessage {
  final String role; // "user", "assistant", "system", "tool"
  final String content;
  final DateTime timestamp;
  final String? toolName;
  final UsageInfo? usage;

  ChatMessage({
    required this.role,
    required this.content,
    DateTime? timestamp,
    this.toolName,
    this.usage,
  }) : timestamp = timestamp ?? DateTime.now();
}

/// Manages the chat session state with ChangeNotifier for Flutter UI.
class SessionManager extends ChangeNotifier {
  final HcpClient client;
  final List<ChatMessage> _messages = [];
  bool _busy = false;
  String _currentResponse = "";
  String? _error;
  UsageInfo? _lastUsage;
  String _serverUrl = "";

  SessionManager({required this.client}) {
    _serverUrl = client.url;
  }

  // ── Getters ────────────────────────────────────────────────────────────

  List<ChatMessage> get messages => List.unmodifiable(_messages);
  bool get busy => _busy;
  String? get error => _error;
  UsageInfo? get lastUsage => _lastUsage;
  String get serverUrl => _serverUrl;

  set serverUrl(String url) {
    _serverUrl = url;
    notifyListeners();
  }

  // ── Connection ─────────────────────────────────────────────────────────

  Future<bool> connect() async {
    try {
      await client.connect();
      return true;
    } catch (e) {
      _error = "连接失败: $e";
      notifyListeners();
      return false;
    }
  }

  void disconnect() {
    client.disconnect();
    notifyListeners();
  }

  // ── Chat actions ───────────────────────────────────────────────────────

  /// Send a message and process the response stream.
  Future<void> sendMessage(String text) async {
    if (text.trim().isEmpty || _busy) return;
    _error = null;

    // Add user message.
    _messages.add(ChatMessage(role: "user", content: text.trim()));
    _busy = true;
    _currentResponse = "";
    notifyListeners();

    try {
      await client.run(text.trim(),
          autoApprove: true,
          onEvent: (event) {
        _handleEvent(event);
      });
    } catch (e) {
      _error = "发送失败: $e";
    }

    // Flush remaining response.
    if (_currentResponse.isNotEmpty) {
      _messages.add(ChatMessage(
          role: "assistant", content: _currentResponse, usage: _lastUsage));
      _currentResponse = "";
    }

    _busy = false;
    notifyListeners();
  }

  void _handleEvent(AgentEvent event) {
    if (event.isText) {
      _currentResponse += event.text;
      notifyListeners();
    } else if (event.isMessage) {
      // Complete message — flush accumulated text.
      if (_currentResponse.isNotEmpty) {
        _messages.add(ChatMessage(
            role: "assistant", content: _currentResponse, usage: _lastUsage));
        _currentResponse = "";
      }
      notifyListeners();
    } else if (event.isToolDispatch && event.tool != null) {
      _messages.add(ChatMessage(
        role: "tool",
        content: "🔧 ${event.tool!.name}(${event.tool!.args})",
        toolName: event.tool!.name,
      ));
      notifyListeners();
    } else if (event.isToolResult && event.tool != null) {
      if (event.tool!.err.isNotEmpty) {
        _messages.add(ChatMessage(
          role: "tool",
          content: "❌ ${event.tool!.name}: ${event.tool!.err}",
          toolName: event.tool!.name,
        ));
      }
      notifyListeners();
    } else if (event.isUsage && event.usage != null) {
      _lastUsage = event.usage;
    } else if (event.isError) {
      _error = event.err;
      notifyListeners();
    } else if (event.isCancelled) {
      _messages.add(ChatMessage(role: "system", content: "⏹ 已取消"));
      notifyListeners();
    }
  }

  /// Cancel the current turn.
  void cancel() {
    client.cancel();
    _busy = false;
    notifyListeners();
  }

  /// Start a new conversation.
  void newSession() {
    client.newSession();
    _messages.clear();
    _currentResponse = "";
    _error = null;
    _lastUsage = null;
    notifyListeners();
  }

  @override
  void dispose() {
    client.dispose();
    super.dispose();
  }
}
