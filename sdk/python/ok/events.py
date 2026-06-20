"""
OK Agent event types — mirror the wireEvent structure from the SSE transport.

Every event from the agent arrives as a JSON object with a "kind" field.
The Event class parses this and exposes typed attributes so callers can
match on event.kind and access kind-specific fields without manual JSON
digging.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Optional


# ── Kind constants ──────────────────────────────────────────────────────────

TURN_STARTED = "turn_started"
REASONING = "reasoning"
TEXT = "text"
MESSAGE = "message"
TOOL_DISPATCH = "tool_dispatch"
TOOL_RESULT = "tool_result"
USAGE = "usage"
APPROVAL = "approval"
ASK = "ask"
ERROR_EVENT = "error"
DONE = "done"
CANCELLED = "cancelled"

# ── Typed sub-structures ────────────────────────────────────────────────────


@dataclass
class ToolCall:
    """A tool dispatch or result."""
    id: str = ""
    name: str = ""
    args: str = ""
    output: str = ""
    err: str = ""
    read_only: bool = False
    truncated: bool = False
    partial: bool = False
    parent_id: str = ""

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> ToolCall:
        return cls(
            id=d.get("id", ""),
            name=d.get("name", ""),
            args=d.get("args", ""),
            output=d.get("output", ""),
            err=d.get("err", ""),
            read_only=d.get("readOnly", False),
            truncated=d.get("truncated", False),
            partial=d.get("partial", False),
            parent_id=d.get("parentId", ""),
        )


@dataclass
class Usage:
    """Token usage information."""
    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0
    cache_hit_tokens: int = 0
    cache_miss_tokens: int = 0
    reasoning_tokens: int = 0
    session_cache_hit_tokens: int = 0
    session_cache_miss_tokens: int = 0
    cost_usd: float = 0.0

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> Usage:
        return cls(
            prompt_tokens=d.get("promptTokens", 0),
            completion_tokens=d.get("completionTokens", 0),
            total_tokens=d.get("totalTokens", 0),
            cache_hit_tokens=d.get("cacheHitTokens", 0),
            cache_miss_tokens=d.get("cacheMissTokens", 0),
            reasoning_tokens=d.get("reasoningTokens", 0),
            session_cache_hit_tokens=d.get("sessionCacheHitTokens", 0),
            session_cache_miss_tokens=d.get("sessionCacheMissTokens", 0),
            cost_usd=d.get("costUsd", 0.0),
        )


@dataclass
class ApprovalRequest:
    """An approval request for a tool call."""
    id: str = ""
    tool: str = ""
    subject: str = ""

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> ApprovalRequest:
        return cls(
            id=d.get("id", ""),
            tool=d.get("tool", ""),
            subject=d.get("subject", ""),
        )


@dataclass
class AskOption:
    """One multiple-choice option in an ask question."""
    label: str = ""
    description: str = ""

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> AskOption:
        return cls(
            label=d.get("label", ""),
            description=d.get("description", ""),
        )


@dataclass
class AskQuestion:
    """A multiple-choice question for the user."""
    id: str = ""
    header: str = ""
    prompt: str = ""
    options: list[AskOption] = field(default_factory=list)
    multi: bool = False

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> AskQuestion:
        return cls(
            id=d.get("id", ""),
            header=d.get("header", ""),
            prompt=d.get("prompt", ""),
            options=[AskOption.from_dict(o) for o in d.get("options", [])],
            multi=d.get("multi", False),
        )


@dataclass
class AskState:
    """An ask event containing one or more questions."""
    id: str = ""
    questions: list[AskQuestion] = field(default_factory=list)

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> AskState:
        return cls(
            id=d.get("id", ""),
            questions=[AskQuestion.from_dict(q) for q in d.get("questions", [])],
        )


# ── Main Event type ─────────────────────────────────────────────────────────


@dataclass
class Event:
    """A typed event from the OK agent's SSE stream.

    Every event has a ``kind`` field you can match against the module-level
    constants (e.g. ``Event.TEXT``). Kind-specific data is populated in the
    corresponding attribute; all others are ``None`` / empty.

    Usage::

        for event in client.stream():
            if event.kind == Event.TEXT:
                print(event.text, end="")
            elif event.kind == Event.USAGE:
                print(f"Tokens: {event.usage.total_tokens}")
    """

    # Kind constants at class level for convenience
    TURN_STARTED = TURN_STARTED
    REASONING = REASONING
    TEXT = TEXT
    MESSAGE = MESSAGE
    TOOL_DISPATCH = TOOL_DISPATCH
    TOOL_RESULT = TOOL_RESULT
    USAGE = USAGE
    APPROVAL = APPROVAL
    ASK = ASK
    ERROR = ERROR_EVENT
    DONE = DONE
    CANCELLED = CANCELLED

    kind: str = ""
    text: str = ""
    reasoning: str = ""
    level: str = ""
    tool: Optional[ToolCall] = None
    usage: Optional[Usage] = None
    approval: Optional[ApprovalRequest] = None
    ask: Optional[AskState] = None
    err: str = ""

    def __bool__(self) -> bool:
        """An Event is truthy when it has a recognised kind."""
        return bool(self.kind)

    @classmethod
    def from_json(cls, raw: str) -> Event:
        """Parse a JSON string (the ``data:`` line of an SSE frame) into an Event."""
        try:
            d: dict[str, Any] = json.loads(raw)
        except json.JSONDecodeError:
            return cls(kind="__parse_error__", err=f"invalid JSON: {raw[:200]}")

        e = cls(
            kind=d.get("kind", ""),
            text=d.get("text", ""),
            reasoning=d.get("reasoning", ""),
            level=d.get("level", ""),
            err=d.get("err", ""),
        )

        if "tool" in d and d["tool"]:
            e.tool = ToolCall.from_dict(d["tool"])
        if "usage" in d and d["usage"]:
            e.usage = Usage.from_dict(d["usage"])
        if "approval" in d and d["approval"]:
            e.approval = ApprovalRequest.from_dict(d["approval"])
        if "ask" in d and d["ask"]:
            e.ask = AskState.from_dict(d["ask"])

        return e
