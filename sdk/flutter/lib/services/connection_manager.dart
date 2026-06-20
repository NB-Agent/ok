/// Connection manager — manages multiple server profiles.
library;

import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../models/server_profile.dart';

/// Manages a list of saved server profiles with persistence.
class ConnectionManager extends ChangeNotifier {
  static const _key = 'ok_server_profiles';
  static const _lastKey = 'ok_last_server_id';

  List<ServerProfile> _profiles = [];
  ServerProfile? _active;
  bool _loaded = false;
  String? _error;

  // ── Getters ─────────────────────────────────────────────────────────

  List<ServerProfile> get profiles => List.unmodifiable(_profiles);
  ServerProfile? get active => _active;
  bool get loaded => _loaded;
  String? get error => _error;
  bool get hasActive => _active != null;

  // ── Load / Save ────────────────────────────────────────────────────

  /// Load profiles from SharedPreferences.
  Future<void> load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_key);
      if (raw != null) {
        final list = jsonDecode(raw) as List<dynamic>;
        _profiles = list
            .map((e) => ServerProfile.fromJson(e as Map<String, dynamic>))
            .toList();
      }
      // Load last active server.
      final lastId = prefs.getString(_lastKey);
      if (lastId != null) {
        _active = _profiles.cast<ServerProfile?>().firstWhere(
              (p) => p?.id == lastId,
              orElse: () => null,
            );
      }
      _loaded = true;
      notifyListeners();
    } catch (e) {
      _error = "加载失败: $e";
      _loaded = true;
      notifyListeners();
    }
  }

  Future<void> _save() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = jsonEncode(_profiles.map((p) => p.toJson()).toList());
      await prefs.setString(_key, raw);
      if (_active != null) {
        await prefs.setString(_lastKey, _active!.id);
      } else {
        await prefs.remove(_lastKey);
      }
    } catch (e) {
      _error = "保存失败: $e";
      notifyListeners();
    }
  }

  // ── CRUD ───────────────────────────────────────────────────────────

  /// Add a new server profile.
  Future<void> add(ServerProfile profile) async {
    _profiles.add(profile);
    await _save();
    notifyListeners();
  }

  /// Remove a server profile.
  Future<void> remove(String id) async {
    _profiles.removeWhere((p) => p.id == id);
    if (_active?.id == id) {
      _active = _profiles.isNotEmpty ? _profiles.first : null;
    }
    await _save();
    notifyListeners();
  }

  /// Update a server profile.
  Future<void> update(ServerProfile profile) async {
    final idx = _profiles.indexWhere((p) => p.id == profile.id);
    if (idx >= 0) {
      _profiles[idx] = profile;
      if (_active?.id == profile.id) {
        _active = profile;
      }
      await _save();
      notifyListeners();
    }
  }

  /// Set the active server to connect to.
  Future<void> setActive(String id) async {
    final profile = _profiles.cast<ServerProfile?>().firstWhere(
          (p) => p?.id == id,
          orElse: () => null,
        );
    _active = profile;
    await _save();
    notifyListeners();
  }

  /// Quick-connect: add a profile and set it active.
  Future<void> connectTo(ServerProfile profile) async {
    // Check if this server already exists (by host+port).
    final existing = _profiles.cast<ServerProfile?>().firstWhere(
          (p) => p?.host == profile.host && p?.port == profile.port,
          orElse: () => null,
        );
    if (existing != null) {
      await setActive(existing.id);
    } else {
      _profiles.add(profile);
      _active = profile;
      await _save();
      notifyListeners();
    }
  }

  /// Disconnect the active server.
  void disconnect() {
    _active = null;
    notifyListeners();
  }
}
