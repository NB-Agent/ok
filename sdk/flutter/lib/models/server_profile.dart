/// Server profile — connection configuration for a remote OK Agent.
library;

import 'dart:convert';
import 'package:uuid/uuid.dart';

const _uuid = Uuid();

/// Connection configuration for one OK Agent server.
class ServerProfile {
  final String id;
  String name;
  String host;
  int port;
  bool useTls;
  String apiKey;
  final DateTime createdAt;

  ServerProfile({
    String? id,
    this.name = "",
    this.host = "127.0.0.1",
    this.port = 3030,
    this.useTls = false,
    this.apiKey = "",
    DateTime? createdAt,
  })  : id = id ?? _uuid.v4(),
        createdAt = createdAt ?? DateTime.now();

  /// WebSocket URL for this server.
  String get wsUrl {
    final scheme = useTls ? 'wss' : 'ws';
    var url = '$scheme://$host:$port/ws';
    if (apiKey.isNotEmpty) {
      url += '?token=$apiKey';
    }
    return url;
  }

  /// HTTP base URL (for health checks).
  String get httpBaseUrl {
    final scheme = useTls ? 'https' : 'http';
    return '$scheme://$host:$port';
  }

  /// Display label.
  String get label => name.isNotEmpty ? name : '$host:$port';

  // ── Serialization ───────────────────────────────────────────────────

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'host': host,
        'port': port,
        'useTls': useTls,
        'apiKey': apiKey,
        'createdAt': createdAt.toIso8601String(),
      };

  factory ServerProfile.fromJson(Map<String, dynamic> json) => ServerProfile(
        id: json['id'] as String?,
        name: json['name'] as String? ?? '',
        host: json['host'] as String? ?? '127.0.0.1',
        port: json['port'] as int? ?? 3030,
        useTls: json['useTls'] as bool? ?? false,
        apiKey: json['apiKey'] as String? ?? '',
        createdAt: json['createdAt'] != null
            ? DateTime.parse(json['createdAt'] as String)
            : null,
      );

  /// Create a profile from a QR code JSON map.
  factory ServerProfile.fromQrCode(Map<String, dynamic> json) => ServerProfile(
        name: json['n'] as String? ?? '',
        host: json['h'] as String? ?? '127.0.0.1',
        port: json['p'] as int? ?? 3030,
        useTls: json['t'] as bool? ?? false,
        apiKey: json['k'] as String? ?? '',
      );

  /// Encode as QR code JSON (compact field names for smaller QR).
  Map<String, dynamic> toQrCode() => {
        'v': 1,
        'n': name,
        'h': host,
        'p': port,
        't': useTls,
        'k': apiKey,
      };

  ServerProfile copyWith({
    String? name,
    String? host,
    int? port,
    bool? useTls,
    String? apiKey,
  }) =>
      ServerProfile(
        id: id,
        name: name ?? this.name,
        host: host ?? this.host,
        port: port ?? this.port,
        useTls: useTls ?? this.useTls,
        apiKey: apiKey ?? this.apiKey,
        createdAt: createdAt,
      );
}
