/// Server add screen — manually add or scan QR code for a server.
library;

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/server_profile.dart';
import '../services/connection_manager.dart';

class ServerAddScreen extends StatefulWidget {
  const ServerAddScreen({super.key});

  @override
  State<ServerAddScreen> createState() => _ServerAddScreenState();
}

class _ServerAddScreenState extends State<ServerAddScreen> {
  final _formKey = GlobalKey<FormState>();
  final _nameCtrl = TextEditingController();
  final _hostCtrl = TextEditingController(text: "192.168.1.");
  final _portCtrl = TextEditingController(text: "3030");
  final _apiKeyCtrl = TextEditingController();
  bool _useTls = false;

  @override
  void dispose() {
    _nameCtrl.dispose();
    _hostCtrl.dispose();
    _portCtrl.dispose();
    _apiKeyCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text("添加服务器"),
        centerTitle: true,
      ),
      body: SingleChildScrollView(
        padding: const EdgeInsets.all(24),
        child: Form(
          key: _formKey,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // QR scan button
              // (QR scanning requires mobile_camera plugin — placeholder)
              OutlinedButton.icon(
                onPressed: () {
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(content: Text("扫码功能需要安装 qr_code_scanner 插件")),
                  );
                },
                icon: const Icon(Icons.qr_code_scanner),
                label: const Text("扫描二维码"),
                style: OutlinedButton.styleFrom(
                  padding: const EdgeInsets.symmetric(vertical: 16),
                ),
              ),
              const SizedBox(height: 24),
              const Text("或手动输入",
                  style: TextStyle(fontWeight: FontWeight.w500)),
              const SizedBox(height: 16),

              // Name
              TextFormField(
                controller: _nameCtrl,
                decoration: const InputDecoration(
                  labelText: "名称 (可选)",
                  hintText: "例如: 家里服务器",
                  border: OutlineInputBorder(),
                  prefixIcon: Icon(Icons.label),
                ),
              ),
              const SizedBox(height: 16),

              // Host
              TextFormField(
                controller: _hostCtrl,
                decoration: const InputDecoration(
                  labelText: "服务器地址",
                  hintText: "192.168.1.100 或 example.com",
                  border: OutlineInputBorder(),
                  prefixIcon: Icon(Icons.dns),
                ),
                validator: (v) =>
                    (v == null || v.trim().isEmpty) ? "请输入服务器地址" : null,
              ),
              const SizedBox(height: 16),

              // Port
              TextFormField(
                controller: _portCtrl,
                decoration: const InputDecoration(
                  labelText: "端口",
                  hintText: "3030",
                  border: OutlineInputBorder(),
                  prefixIcon: Icon(Icons.numbers),
                ),
                keyboardType: TextInputType.number,
                validator: (v) {
                  if (v == null || v.trim().isEmpty) return "请输入端口";
                  final port = int.tryParse(v.trim());
                  if (port == null || port < 1 || port > 65535) {
                    return "端口范围 1-65535";
                  }
                  return null;
                },
              ),
              const SizedBox(height: 16),

              // API Key
              TextFormField(
                controller: _apiKeyCtrl,
                decoration: const InputDecoration(
                  labelText: "API Key (可选)",
                  hintText: "ok_xxxxxxxx...",
                  border: OutlineInputBorder(),
                  prefixIcon: Icon(Icons.key),
                  helperText: "服务器设置了 --api-key 时需要",
                ),
                obscureText: true,
              ),
              const SizedBox(height: 16),

              // TLS switch
              SwitchListTile(
                title: const Text("使用 TLS (WSS)"),
                subtitle: const Text("服务器配置了 TLS 证书时启用"),
                value: _useTls,
                onChanged: (v) => setState(() => _useTls = v),
              ),
              const SizedBox(height: 32),

              // Save button
              FilledButton.icon(
                onPressed: _save,
                icon: const Icon(Icons.save),
                label: const Text("保存并连接"),
                style: FilledButton.styleFrom(
                  padding: const EdgeInsets.symmetric(vertical: 16),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  void _save() async {
    if (!_formKey.currentState!.validate()) return;

    final profile = ServerProfile(
      name: _nameCtrl.text.trim(),
      host: _hostCtrl.text.trim(),
      port: int.parse(_portCtrl.text.trim()),
      useTls: _useTls,
      apiKey: _apiKeyCtrl.text.trim(),
    );

    final cm = context.read<ConnectionManager>();
    await cm.connectTo(profile);

    if (!mounted) return;
    Navigator.of(context).pop(); // back to server list
  }
}
