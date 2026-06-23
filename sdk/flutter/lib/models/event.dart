/// Typed event models matching the HCP WebSocket protocol (wireEvent format).
///
/// Every event from the OK agent is a JSON object with a "kind" field.
/// This file defines the Event class hierarchy for type-safe parsing.
library;

import 'dart:convert';

// ── Kind constants ────────────────────────────────────────────────────────

const String kTurnStarted = "turn_started";
const String kReasoning = "reasoning";
const String kText = "text";
const String kMessage = "message";
const String kToolDispatch = "tool_dispatch";
const String kToolResult = "tool_result";
const String kUsage = "usage";
const String kApproval = "approval";
const String kAsk = "ask";
const String kError = "error";
const String kDone = "done";
const String kCancelled = "cancelled";
const String kHistory = "history";
const String kContextResponse = "context_response";

// ── Sub-structures ────────────────────────────────────────────────────────

class ToolCall {
  final String id;
  final String name;
  final String args;
  final String output;
  final String err;
  final bool readOnly;
  final bool truncated;
  final bool partial;
  final String parentId;

  ToolCall({
    this.id = "",
    this.name = "",
    this.args = "",
    this.output = "",
    this.err = "",
    this.readOnly = false,
    this.truncated = false,
    this.partial = false,
    this.parentId = "",
  });

  factory ToolCall.fromJson(Map<String, dynamic> json) => ToolCall(
        id: json['id'] as String? ?? "",
        name: json['name'] as String? ?? "",
        args: json['args'] as String? ?? "",
        output: json['output'] as String? ?? "",
        err: json['err'] as String? ?? "",
        readOnly: json['readOnly'] as bool? ?? false,
        truncated: json['truncated'] as bool? ?? false,
        partial: json['partial'] as bool? ?? false,
        parentId: json['parentId'] as String? ?? "",
      );
}

class UsageInfo {
  final int promptTokens;
  final int completionTokens;
  final int totalTokens;
  final int cacheHitTokens;
  final int cacheMissTokens;
  final int reasoningTokens;
  final int sessionCacheHitTokens;
  final int sessionCacheMissTokens;
  final double costUsd;

  UsageInfo({
    this.promptTokens = 0,
    this.completionTokens = 0,
    this.totalTokens = 0,
    this.cacheHitTokens = 0,
    this.cacheMissTokens = 0,
    this.reasoningTokens = 0,
    this.sessionCacheHitTokens = 0,
    this.sessionCacheMissTokens = 0,
    this.costUsd = 0.0,
  });

  factory UsageInfo.fromJson(Map<String, dynamic> json) => UsageInfo(
        promptTokens: json['promptTokens'] as int? ?? 0,
        completionTokens: json['completionTokens'] as int? ?? 0,
        totalTokens: json['totalTokens'] as int? ?? 0,
        cacheHitTokens: json['cacheHitTokens'] as int? ?? 0,
        cacheMissTokens: json['cacheMissTokens'] as int? ?? 0,
        reasoningTokens: json['reasoningTokens'] as int? ?? 0,
        sessionCacheHitTokens: json['sessionCacheHitTokens'] as int? ?? 0,
        sessionCacheMissTokens: json['sessionCacheMissTokens'] as int? ?? 0,
        costUsd: (json['costUsd'] as num?)?.toDouble() ?? 0.0,
      );
}

class ApprovalRequest {
  final String id;
  final String tool;
  final String subject;

  ApprovalRequest({this.id = "", this.tool = "", this.subject = ""});

  factory ApprovalRequest.fromJson(Map<String, dynamic> json) =>
      ApprovalRequest(
        id: json['id'] as String? ?? "",
        tool: json['tool'] as String? ?? "",
        subject: json['subject'] as String? ?? "",
      );
}

class AskOption {
  final String label;
  final String description;

  AskOption({this.label = "", this.description = ""});

  factory AskOption.fromJson(Map<String, dynamic> json) => AskOption(
        label: json['label'] as String? ?? "",
        description: json['description'] as String? ?? "",
      );
}

class AskQuestion {
  final String id;
  final String header;
  final String prompt;
  final List<AskOption> options;
  final bool multi;

  AskQuestion({
    this.id = "",
    this.header = "",
    this.prompt = "",
    this.options = const [],
    this.multi = false,
  });

  factory AskQuestion.fromJson(Map<String, dynamic> json) => AskQuestion(
        id: json['id'] as String? ?? "",
        header: json['header'] as String? ?? "",
        prompt: json['prompt'] as String? ?? "",
        options: (json['options'] as List<dynamic>?)
                ?.map((e) => AskOption.fromJson(e as Map<String, dynamic>))
                .toList() ??
            [],
        multi: json['multi'] as bool? ?? false,
      );
}

class AskState {
  final String id;
  final List<AskQuestion> questions;

  AskState({this.id = "", this.questions = const []});

  factory AskState.fromJson(Map<String, dynamic> json) => AskState(
        id: json['id'] as String? ?? "",
        questions: (json['questions'] as List<dynamic>?)
                ?.map(
                    (e) => AskQuestion.fromJson(e as Map<String, dynamic>))
                .toList() ??
            [],
      );
}

// ── Main Event type ───────────────────────────────────────────────────────

class AgentEvent {
  final String kind;
  final String text;
  final String reasoning;
  final String level;
  final ToolCall? tool;
  final UsageInfo? usage;
  final ApprovalRequest? approval;
  final AskState? ask;
  final String err;
  final List<Map<String, String>>? history;
  final int? contextUsed;
  final int? contextWindow;

  AgentEvent({
    this.kind = "",
    this.text = "",
    this.reasoning = "",
    this.level = "",
    this.tool,
    this.usage,
    this.approval,
    this.ask,
    this.err = "",
    this.history,
    this.contextUsed,
    this.contextWindow,
  });

  bool get isText => kind == kText;
  bool get isMessage => kind == kMessage;
  bool get isDone => kind == kDone;
  bool get isCancelled => kind == kCancelled;
  bool get isError => kind == kError;
  bool get isToolDispatch => kind == kToolDispatch;
  bool get isToolResult => kind == kToolResult;
  bool get isUsage => kind == kUsage;
  bool get isApproval => kind == kApproval;
  bool get isAsk => kind == kAsk;
  bool get isReasoning => kind == kReasoning;
  bool get isHistory => kind == kHistory;

  factory AgentEvent.fromJsonString(String raw) {
    try {
      final d = jsonDecode(raw) as Map<String, dynamic>;
      return AgentEvent(
        kind: d['kind'] as String? ?? "",
        text: d['text'] as String? ?? "",
        reasoning: d['reasoning'] as String? ?? "",
        level: d['level'] as String? ?? "",
        tool: d['tool'] != null
            ? ToolCall.fromJson(d['tool'] as Map<String, dynamic>)
            : null,
        usage: d['usage'] != null
            ? UsageInfo.fromJson(d['usage'] as Map<String, dynamic>)
            : null,
        approval: d['approval'] != null
            ? ApprovalRequest.fromJson(d['approval'] as Map<String, dynamic>)
            : null,
        ask: d['ask'] != null
            ? AskState.fromJson(d['ask'] as Map<String, dynamic>)
            : null,
        err: d['err'] as String? ?? "",
        history: d['messages'] != null
            ? (d['messages'] as List<dynamic>)
                .map((m) => Map<String, String>.from(m as Map))
                .toList()
            : null,
        contextUsed: d['used'] as int?,
        contextWindow: d['window'] as int?,
      );
    } catch (_) {
      return AgentEvent(kind: "__parse_error__", err: "Invalid JSON: $raw");
    }
  }
}

// ── Command types (client → server) ──────────────────────────────────────

Map<String, dynamic> submitCommand(String text) =>
    {"type": "submit", "input": text};

Map<String, dynamic> cancelCommand() => {"type": "cancel"};

Map<String, dynamic> approveCommand(String id,
        {bool allow = true, bool session = false}) =>
    {"type": "approve", "id": id, "allow": allow, "session": session};

Map<String, dynamic> answerCommand(
    String id, List<Map<String, dynamic>> answers) {
  return {"type": "answer", "id": id, "answers": answers};
}

Map<String, dynamic> planCommand(bool on) => {"type": "plan", "on": on};

Map<String, dynamic> newSessionCommand() => {"type": "new_session"};

Map<String, dynamic> compactCommand() => {"type": "compact"};

Map<String, dynamic> historyCommand() => {"type": "history"};

Map<String, dynamic> contextCommand() => {"type": "context"};
