/// Chat screen — the main conversation UI for the OK Agent.
library;

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:flutter_markdown/flutter_markdown.dart';

import '../models/event.dart';
import '../services/session_manager.dart';

class ChatScreen extends StatefulWidget {
  const ChatScreen({super.key});

  @override
  State<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScreenState extends State<ChatScreen> {
  final TextEditingController _inputController = TextEditingController();
  final ScrollController _scrollController = ScrollController();
  bool _showSettings = false;

  @override
  void dispose() {
    _inputController.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollController.hasClients) {
        _scrollController.animateTo(
          _scrollController.position.maxScrollExtent,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        );
      }
    });
  }

  void _sendMessage(SessionManager sm) {
    final text = _inputController.text;
    if (text.trim().isEmpty) return;
    _inputController.clear();
    sm.sendMessage(text).then((_) => _scrollToBottom());
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<SessionManager>(
      builder: (context, sm, _) => Scaffold(
        appBar: _buildAppBar(context, sm),
        body: Column(
          children: [
            // Connection status bar.
            _buildStatusBar(sm),

            // Settings panel (toggleable).
            if (_showSettings) _buildSettingsPanel(sm),

            // Messages list.
            Expanded(child: _buildMessageList(sm)),

            // Error banner.
            if (sm.error != null) _buildErrorBanner(sm),

            // Usage bar.
            if (sm.lastUsage != null) _buildUsageBar(sm),

            // Input area.
            _buildInputArea(sm),
          ],
        ),
      ),
    );
  }

  PreferredSizeWidget _buildAppBar(BuildContext context, SessionManager sm) {
    return AppBar(
      title: const Text("OK Agent"),
      centerTitle: true,
      actions: [
        // New session.
        IconButton(
          icon: const Icon(Icons.add_comment),
          tooltip: "新建对话",
          onPressed: () => sm.newSession(),
        ),
        // Settings.
        IconButton(
          icon: Icon(_showSettings ? Icons.settings : Icons.settings_outlined),
          tooltip: "设置",
          onPressed: () => setState(() => _showSettings = !_showSettings),
        ),
      ],
    );
  }

  Widget _buildStatusBar(SessionManager sm) {
    final connected = sm.client.status == ConnectionStatus.connected;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      color: connected ? Colors.green.shade50 : Colors.orange.shade50,
      child: Row(
        children: [
          Icon(
            connected ? Icons.cloud_done : Icons.cloud_off,
            size: 16,
            color: connected ? Colors.green : Colors.orange,
          ),
          const SizedBox(width: 8),
          Text(
            connected ? "已连接 (${sm.serverUrl})" : "未连接",
            style: TextStyle(
              fontSize: 12,
              color: connected ? Colors.green.shade700 : Colors.orange.shade700,
            ),
          ),
          const Spacer(),
          if (sm.busy)
            const SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
        ],
      ),
    );
  }

  Widget _buildSettingsPanel(SessionManager sm) {
    return Container(
      padding: const EdgeInsets.all(16),
      color: Colors.grey.shade50,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text("服务器设置", style: TextStyle(fontWeight: FontWeight.bold)),
          const SizedBox(height: 8),
          TextField(
            decoration: const InputDecoration(
              labelText: "WebSocket URL",
              hintText: "ws://127.0.0.1:3030/ws",
              border: OutlineInputBorder(),
              isDense: true,
            ),
            controller: TextEditingController(text: sm.serverUrl),
            onSubmitted: (value) {
              sm.serverUrl = value;
              sm.disconnect();
              sm.connect();
            },
          ),
          const SizedBox(height: 8),
          Text(
            "需要在服务器启动: ok serve",
            style: TextStyle(fontSize: 12, color: Colors.grey.shade600),
          ),
        ],
      ),
    );
  }

  Widget _buildMessageList(SessionManager sm) {
    if (sm.messages.isEmpty && !sm.busy) {
      return const Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(Icons.chat_bubble_outline, size: 64, color: Colors.grey),
            SizedBox(height: 16),
            Text("发送消息开始与 OK Agent 对话",
                style: TextStyle(color: Colors.grey, fontSize: 16)),
          ],
        ),
      );
    }

    return ListView.builder(
      controller: _scrollController,
      padding: const EdgeInsets.all(12),
      itemCount: sm.messages.length + (sm._currentResponse.isNotEmpty ? 1 : 0),
      itemBuilder: (context, index) {
        if (index < sm.messages.length) {
          return _MessageBubble(message: sm.messages[index]);
        }
        // Current streaming response.
        return _MessageBubble(
          message: ChatMessage(role: "assistant", content: sm._currentResponse),
          isStreaming: true,
        );
      },
    );
  }

  Widget _buildErrorBanner(SessionManager sm) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(8),
      color: Colors.red.shade50,
      child: Row(
        children: [
          Expanded(
            child: Text(sm.error!,
                style: TextStyle(color: Colors.red.shade700, fontSize: 13)),
          ),
          IconButton(
            icon: const Icon(Icons.close, size: 18),
            onPressed: () => sm.notifyListeners(), // triggers rebuild
          ),
        ],
      ),
    );
  }

  Widget _buildUsageBar(SessionManager sm) {
    final u = sm.lastUsage!;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      color: Colors.blue.shade50,
      child: Row(
        children: [
          Icon(Icons.token, size: 14, color: Colors.blue.shade600),
          const SizedBox(width: 4),
          Text("Tokens: ${u.totalTokens}",
              style:
                  TextStyle(fontSize: 11, color: Colors.blue.shade700)),
          const Spacer(),
          if (u.costUsd > 0)
            Text("\$${u.costUsd.toStringAsFixed(4)}",
                style:
                    TextStyle(fontSize: 11, color: Colors.blue.shade700)),
        ],
      ),
    );
  }

  Widget _buildInputArea(SessionManager sm) {
    return Container(
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        color: Theme.of(context).scaffoldBackgroundColor,
        boxShadow: [
          BoxShadow(
            color: Colors.black.withOpacity(0.05),
            blurRadius: 4,
            offset: const Offset(0, -2),
          ),
        ],
      ),
      child: SafeArea(
        child: Row(
          children: [
            // Cancel button when busy.
            if (sm.busy)
              IconButton(
                icon: const Icon(Icons.stop_circle, color: Colors.red),
                onPressed: () => sm.cancel(),
              ),
            // Text input.
            Expanded(
              child: TextField(
                controller: _inputController,
                enabled: !sm.busy,
                textInputAction: TextInputAction.send,
                decoration: InputDecoration(
                  hintText: sm.busy ? "等待响应..." : "输入消息...",
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(24),
                  ),
                  contentPadding: const EdgeInsets.symmetric(
                      horizontal: 16, vertical: 10),
                  filled: true,
                  fillColor: Colors.grey.shade50,
                ),
                maxLines: 4,
                minLines: 1,
                onSubmitted: sm.busy ? null : (_) => _sendMessage(sm),
              ),
            ),
            const SizedBox(width: 8),
            // Send button.
            IconButton.filled(
              icon: const Icon(Icons.send_rounded),
              onPressed: sm.busy ? null : () => _sendMessage(sm),
              style: IconButton.styleFrom(
                backgroundColor: Theme.of(context).colorScheme.primary,
                foregroundColor: Theme.of(context).colorScheme.onPrimary,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ── Message Bubble Widget ─────────────────────────────────────────────────

class _MessageBubble extends StatelessWidget {
  final ChatMessage message;
  final bool isStreaming;

  const _MessageBubble({
    required this.message,
    this.isStreaming = false,
  });

  @override
  Widget build(BuildContext context) {
    final isUser = message.role == "user";
    final isTool = message.role == "tool";
    final isSystem = message.role == "system";

    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Row(
        mainAxisAlignment:
            isUser ? MainAxisAlignment.end : MainAxisAlignment.start,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Avatar for assistant messages.
          if (!isUser) _buildAvatar(context, isTool, isSystem),
          Flexible(
            child: Container(
              constraints: BoxConstraints(
                maxWidth: MediaQuery.of(context).size.width * 0.75,
              ),
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: _bubbleColor(context, isUser, isTool, isSystem),
                borderRadius: BorderRadius.circular(16).copyWith(
                  bottomLeft: isUser ? const Radius.circular(16) : Radius.zero,
                  bottomRight:
                      isUser ? Radius.zero : const Radius.circular(16),
                ),
              ),
              child: _buildContent(context),
            ),
          ),
          // Avatar for user messages (right side).
          if (isUser) _buildAvatar(context, false, false),
        ],
      ),
    );
  }

  Widget _buildAvatar(BuildContext context, bool isTool, bool isSystem) {
    return Padding(
      padding: const EdgeInsets.all(8),
      child: CircleAvatar(
        radius: 16,
        backgroundColor: isTool
            ? Colors.orange.shade100
            : isSystem
                ? Colors.grey.shade200
                : Theme.of(context).colorScheme.primaryContainer,
        child: Icon(
          isTool
              ? Icons.build
              : isSystem
                  ? Icons.info
                  : Icons.smart_toy,
          size: 18,
          color: isTool
              ? Colors.orange.shade700
              : isSystem
                  ? Colors.grey.shade600
                  : Theme.of(context).colorScheme.onPrimaryContainer,
        ),
      ),
    );
  }

  Color _bubbleColor(
      BuildContext context, bool isUser, bool isTool, bool isSystem) {
    if (isUser) return Theme.of(context).colorScheme.primaryContainer;
    if (isTool) return Colors.orange.shade50;
    if (isSystem) return Colors.grey.shade100;
    return Theme.of(context).colorScheme.surfaceContainerHighest;
  }

  Widget _buildContent(BuildContext context) {
    if (message.role == "tool") {
      return Text(
        message.content,
        style: TextStyle(
          fontFamily: 'monospace',
          fontSize: 12,
          color: Colors.orange.shade800,
        ),
      );
    }

    if (message.role == "system") {
      return Text(
        message.content,
        style: TextStyle(
          fontSize: 13,
          fontStyle: FontStyle.italic,
          color: Colors.grey.shade600,
        ),
      );
    }

    // User or assistant — render with Markdown.
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        MarkdownBody(
          data: message.content,
          styleSheet: MarkdownStyleSheet(
            p: TextStyle(
              fontSize: 15,
              color: message.role == "user"
                  ? Theme.of(context).colorScheme.onPrimaryContainer
                  : Theme.of(context).colorScheme.onSurface,
            ),
            code: TextStyle(
              fontSize: 13,
              backgroundColor: Colors.grey.shade200,
              fontFamily: 'monospace',
            ),
            codeblockDecoration: BoxDecoration(
              color: Colors.grey.shade100,
              borderRadius: BorderRadius.circular(8),
            ),
          ),
          selectable: true,
        ),
        if (isStreaming)
          const SizedBox(
            width: 12,
            height: 12,
            child: Center(
              child: SizedBox(
                width: 8,
                height: 8,
                child: CircularProgressIndicator(strokeWidth: 2),
              ),
            ),
          ),
        if (message.usage != null)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Text(
              "⚡ ${message.usage!.totalTokens} tokens${message.usage!.costUsd > 0 ? ' · \$${message.usage!.costUsd.toStringAsFixed(4)}' : ''}",
              style: TextStyle(fontSize: 10, color: Colors.grey.shade500),
            ),
          ),
      ],
    );
  }
}
