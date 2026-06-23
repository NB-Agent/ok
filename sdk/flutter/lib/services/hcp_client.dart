/// HCP WebSocket client — connects to OK Agent's /ws endpoint.
///
/// Handles connection lifecycle, command sending, and event streaming.
library;

import 'dart:async';
import 'dart:convert';

import 'package:web_socket_channel/web_socket_channel.dart';

import '../models/event.dart';

/// Connection state for UI display.
enum ConnectionStatus { disconnected, connecting, connected, error }

/// Callback for incoming agent events.
typedef EventHandler = void Function(AgentEvent event);

/// HCP WebSocket client for the OK Agent protocol.
class HcpClient {
  final String url;
  final Duration timeout;
  final Duration pingInterval;

  WebSocketChannel? _channel;
  StreamSubscription? _subscription;
  Timer? _pingTimer;
  bool _disposed = false;

  ConnectionStatus _status = ConnectionStatus.disconnected;
  final StreamController<ConnectionStatus> _statusController =
      StreamController<ConnectionStatus>.broadcast();
  final StreamController<AgentEvent> _eventController =
      StreamController<AgentEvent>.broadcast();

  HcpClient({
    this.url = "ws://127.0.0.1:3030/ws",
    this.timeout = const Duration(seconds: 10),
    this.pingInterval = const Duration(seconds: 30),
  });

  /// Create from a ServerProfile.
  factory HcpClient.fromProfile(ServerProfile profile) => HcpClient(
        url: profile.wsUrl,
      );

  // ── Observable streams ─────────────────────────────────────────────────

  Stream<ConnectionStatus> get statusStream => _statusController.stream;
  Stream<AgentEvent> get eventStream => _eventController.stream;
  ConnectionStatus get status => _status;

  // ── Connection lifecycle ───────────────────────────────────────────────

  /// Connect to the OK agent WebSocket endpoint.
  Future<void> connect() async {
    if (_status == ConnectionStatus.connected) return;
    _setStatus(ConnectionStatus.connecting);

    try {
      final wsUrl = Uri.parse(url);
      _channel = WebSocketChannel.connect(wsUrl);

      // Wait for the connection to be ready.
      await _channel!.ready;

      _setStatus(ConnectionStatus.connected);

      // Start reading events.
      _subscription = _channel!.stream.listen(
        (data) {
          final msg = data is String ? data : utf8.decode(data as List<int>);
          final event = AgentEvent.fromJsonString(msg);
          _eventController.add(event);
        },
        onError: (error) {
          _setStatus(ConnectionStatus.error);
        },
        onDone: () {
          if (!_disposed) {
            _setStatus(ConnectionStatus.disconnected);
          }
        },
        cancelOnError: false,
      );

      // Periodic keep-alive ping.
      _pingTimer = Timer.periodic(pingInterval, (_) {
        try {
          _channel?.sink.add(jsonEncode({"type": "ping"}));
        } catch (_) {}
      });
    } catch (e) {
      _setStatus(ConnectionStatus.error);
      rethrow;
    }
  }

  /// Disconnect from the agent.
  void disconnect() {
    _disposed = true;
    _pingTimer?.cancel();
    _subscription?.cancel();
    _channel?.sink.close();
    _channel = null;
    _setStatus(ConnectionStatus.disconnected);
  }

  // ── Send commands ──────────────────────────────────────────────────────

  void _send(Map<String, dynamic> cmd) {
    if (_channel == null || _status != ConnectionStatus.connected) {
      throw StateError("WebSocket not connected");
    }
    _channel!.sink.add(jsonEncode(cmd));
  }

  void submit(String text) => _send(submitCommand(text));
  void cancel() => _send(cancelCommand());
  void approve(String id, {bool allow = true, bool session = false}) =>
      _send(approveCommand(id, allow: allow, session: session));
  void answer(String id, List<Map<String, dynamic>> answers) =>
      _send(answerCommand(id, answers));
  void setPlanMode(bool on) => _send(planCommand(on));
  void newSession() => _send(newSessionCommand());
  void compact() => _send(compactCommand());
  void fetchHistory() => _send(historyCommand());
  void fetchContext() => _send(contextCommand());

  // ── Convenience: submit + wait for done ────────────────────────────────

  /// Submit text and return a Future that completes when the turn is done.
  ///
  /// [onEvent] is called for each event during the turn.
  /// [autoApprove] automatically approves tool calls.
  Future<void> run(String text,
      {void Function(AgentEvent)? onEvent, bool autoApprove = false}) async {
    final completer = Completer<void>();
    StreamSubscription? sub;

    sub = _eventController.stream.listen((event) {
      if (onEvent != null) onEvent(event);

      if (autoApprove && event.isApproval && event.approval != null) {
        approve(event.approval!.id, allow: true);
      }

      if (event.isDone || event.isCancelled || event.isError) {
        if (!completer.isCompleted) completer.complete();
      }
    });

    submit(text);
    await completer.future;
    await sub.cancel();
  }

  // ── Internal ───────────────────────────────────────────────────────────

  void _setStatus(ConnectionStatus s) {
    _status = s;
    if (!_disposed) _statusController.add(s);
  }

  void dispose() {
    disconnect();
    _statusController.close();
    _eventController.close();
  }
}
