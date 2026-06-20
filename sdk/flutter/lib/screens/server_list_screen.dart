/// Server list screen — show saved servers and connect.
library;

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/server_profile.dart';
import '../services/connection_manager.dart';
import 'chat_screen.dart';
import 'server_add_screen.dart';

class ServerListScreen extends StatefulWidget {
  const ServerListScreen({super.key});

  @override
  State<ServerListScreen> createState() => _ServerListScreenState();
}

class _ServerListScreenState extends State<ServerListScreen> {
  @override
  void initState() {
    super.initState();
    // Load saved servers on startup.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      context.read<ConnectionManager>().load();
    });
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<ConnectionManager>(
      builder: (context, cm, _) => Scaffold(
        appBar: AppBar(
          title: const Text("OK Agent"),
          centerTitle: true,
          actions: [
            IconButton(
              icon: const Icon(Icons.add),
              tooltip: "添加服务器",
              onPressed: () => _addServer(context),
            ),
          ],
        ),
        body: _buildBody(context, cm),
        floatingActionButton: FloatingActionButton.extended(
          onPressed: () => _addServer(context),
          icon: const Icon(Icons.add),
          label: const Text("添加服务器"),
        ),
      ),
    );
  }

  Widget _buildBody(BuildContext context, ConnectionManager cm) {
    if (!cm.loaded) {
      return const Center(child: CircularProgressIndicator());
    }

    if (cm.profiles.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(Icons.dns_outlined, size: 80, color: Colors.grey.shade300),
              const SizedBox(height: 24),
              Text(
                "还没有保存的服务器",
                style: TextStyle(
                    fontSize: 20,
                    color: Colors.grey.shade600,
                    fontWeight: FontWeight.w500),
              ),
              const SizedBox(height: 12),
              Text(
                "添加一个 OK Agent 服务器的连接信息\n"
                "服务器启动: ok serve --host 0.0.0.0 --api-key xxx",
                textAlign: TextAlign.center,
                style: TextStyle(fontSize: 14, color: Colors.grey.shade500),
              ),
              const SizedBox(height: 32),
              FilledButton.tonalIcon(
                onPressed: () => _addQuickStart(context),
                icon: const Icon(Icons.flash_on),
                label: const Text("快速连接 (127.0.0.1:3030)"),
              ),
            ],
          ),
        ),
      );
    }

    return ListView.builder(
      padding: const EdgeInsets.all(16),
      itemCount: cm.profiles.length,
      itemBuilder: (context, index) =>
          _buildServerCard(context, cm, cm.profiles[index]),
    );
  }

  Widget _buildServerCard(
      BuildContext context, ConnectionManager cm, ServerProfile profile) {
    final isActive = cm.active?.id == profile.id;
    return Card(
      margin: const EdgeInsets.only(bottom: 12),
      elevation: isActive ? 2 : 0,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(16),
        side: BorderSide(
          color: isActive
              ? Theme.of(context).colorScheme.primary
              : Colors.grey.shade200,
          width: isActive ? 2 : 1,
        ),
      ),
      child: InkWell(
        borderRadius: BorderRadius.circular(16),
        onTap: () => _connect(context, cm, profile),
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Row(
            children: [
              // Status icon
              Container(
                width: 48,
                height: 48,
                decoration: BoxDecoration(
                  color: isActive
                      ? Colors.green.shade50
                      : Colors.grey.shade100,
                  borderRadius: BorderRadius.circular(12),
                ),
                child: Icon(
                  isActive ? Icons.check_circle : Icons.dns,
                  color: isActive ? Colors.green : Colors.grey,
                ),
              ),
              const SizedBox(width: 16),
              // Server info
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      profile.label,
                      style: const TextStyle(
                          fontSize: 16, fontWeight: FontWeight.w600),
                    ),
                    const SizedBox(height: 4),
                    Text(
                      '${profile.host}:${profile.port}'
                      '${profile.useTls ? ' (WSS)' : ''}'
                      '${profile.apiKey.isNotEmpty ? ' 🔑' : ''}',
                      style: TextStyle(
                          fontSize: 13, color: Colors.grey.shade600),
                    ),
                  ],
                ),
              ),
              // Connect button
              FilledButton.tonal(
                onPressed: () => _connect(context, cm, profile),
                child: Text(isActive ? "已连接" : "连接"),
              ),
              const SizedBox(width: 8),
              // Delete
              IconButton(
                icon: Icon(Icons.delete_outline,
                    color: Colors.red.shade300),
                onPressed: () => _confirmDelete(context, cm, profile),
              ),
            ],
          ),
        ),
      ),
    );
  }

  void _connect(BuildContext context, ConnectionManager cm, ServerProfile profile) async {
    await cm.setActive(profile.id);
    if (!mounted) return;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ChangeNotifierProvider.value(
          value: cm,
          child: const ChatScreen(),
        ),
      ),
    );
  }

  void _addServer(BuildContext context) {
    Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => const ServerAddScreen()),
    );
  }

  void _addQuickStart(BuildContext context) async {
    final cm = context.read<ConnectionManager>();
    await cm.connectTo(ServerProfile(
      name: "本地服务器",
      host: "127.0.0.1",
      port: 3030,
    ));
    if (!mounted) return;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ChangeNotifierProvider.value(
          value: cm,
          child: const ChatScreen(),
        ),
      ),
    );
  }

  void _confirmDelete(
      BuildContext context, ConnectionManager cm, ServerProfile profile) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text("删除服务器"),
        content: Text("确定删除「${profile.label}」？"),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text("取消"),
          ),
          FilledButton(
            onPressed: () {
              cm.remove(profile.id);
              Navigator.pop(ctx);
            },
            style: FilledButton.styleFrom(
                backgroundColor: Colors.red),
            child: const Text("删除"),
          ),
        ],
      ),
    );
  }
}
