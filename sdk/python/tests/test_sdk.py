"""Tests for the OK Python SDK — all use in-memory data, no agent connection needed."""
import json
import pytest
from ok import (
    Agent, AgentConfig, RunResult,
    Event, ToolCall, Usage, ApprovalRequest, AskState, AskQuestion, AskOption,
    TEXT, DONE, USAGE, TOOL_DISPATCH, ERROR_EVENT, APPROVAL, ASK,
    TransportConfig, TransportError,
)


class TestEventParsing:
    """Event.from_json() parses all wireEvent shapes correctly."""

    def test_text_event(self):
        raw = json.dumps({"kind": "text", "text": "Hello world"})
        e = Event.from_json(raw)
        assert e.kind == TEXT
        assert e.text == "Hello world"
        assert e.tool is None

    def test_tool_dispatch_event(self):
        raw = json.dumps({
            "kind": "tool_dispatch",
            "tool": {
                "id": "call_123",
                "name": "bash",
                "args": 'echo hi',
                "readOnly": False,
            }
        })
        e = Event.from_json(raw)
        assert e.kind == TOOL_DISPATCH
        assert e.tool is not None
        assert e.tool.name == "bash"
        assert e.tool.id == "call_123"
        assert e.tool.read_only is False

    def test_usage_event(self):
        raw = json.dumps({
            "kind": "usage",
            "usage": {
                "promptTokens": 100,
                "completionTokens": 50,
                "totalTokens": 150,
                "cacheHitTokens": 20,
                "cacheMissTokens": 80,
                "costUsd": 0.002,
            }
        })
        e = Event.from_json(raw)
        assert e.kind == USAGE
        assert e.usage is not None
        assert e.usage.prompt_tokens == 100
        assert e.usage.total_tokens == 150
        assert e.usage.cost_usd == 0.002

    def test_approval_event(self):
        raw = json.dumps({
            "kind": "approval",
            "approval": {
                "id": "appr_1",
                "tool": "write_file",
                "subject": "Write to /etc/passwd",
            }
        })
        e = Event.from_json(raw)
        assert e.kind == APPROVAL
        assert e.approval is not None
        assert e.approval.id == "appr_1"
        assert e.approval.tool == "write_file"

    def test_ask_event(self):
        raw = json.dumps({
            "kind": "ask",
            "ask": {
                "id": "ask_1",
                "questions": [{
                    "id": "q1",
                    "header": "Choice",
                    "prompt": "Pick one:",
                    "options": [
                        {"label": "A", "description": "Option A"},
                        {"label": "B"},
                    ],
                    "multi": False,
                }]
            }
        })
        e = Event.from_json(raw)
        assert e.kind == ASK
        assert e.ask is not None
        assert len(e.ask.questions) == 1
        q = e.ask.questions[0]
        assert q.header == "Choice"
        assert len(q.options) == 2
        assert q.options[0].label == "A"
        assert q.options[0].description == "Option A"

    def test_done_event(self):
        e = Event.from_json('{"kind": "done"}')
        assert e.kind == DONE

    def test_error_event(self):
        raw = json.dumps({"kind": "error", "err": "something broke"})
        e = Event.from_json(raw)
        assert e.kind == ERROR_EVENT
        assert e.err == "something broke"

    def test_invalid_json_returns_parse_error(self):
        e = Event.from_json("not json")
        assert e.kind == "__parse_error__"

    def test_empty_event_is_falsey(self):
        e = Event()
        assert not e


class TestRunResult:
    """RunResult aggregates correctly."""

    def test_text_concatenation(self):
        r = RunResult()
        r.text_parts.append("Hello ")
        r.text_parts.append("world")
        assert r.text == "Hello world"

    def test_repr(self):
        r = RunResult()
        assert repr(r) == "<RunResult>"
        r.text_parts.append("Hello")
        assert "Hello" in repr(r)

    def test_tool_calls_empty(self):
        r = RunResult()
        assert r.tool_calls == []
        assert not r.cancelled


class TestToolCall:
    def test_from_dict(self):
        t = ToolCall.from_dict({
            "name": "grep",
            "args": "-r foo",
            "output": "foo.txt:1:foo",
            "readOnly": True,
            "id": "call_1",
        })
        assert t.name == "grep"
        assert t.args == "-r foo"
        assert t.read_only is True
        assert t.err == ""


class TestUsage:
    def test_from_dict(self):
        u = Usage.from_dict({
            "promptTokens": 200,
            "totalTokens": 300,
            "costUsd": 0.005,
        })
        assert u.prompt_tokens == 200
        assert u.total_tokens == 300
        assert u.cost_usd == 0.005


class TestTransportConfig:
    def test_defaults(self):
        c = TransportConfig()
        assert c.base_url == "http://127.0.0.1:3030"
        assert c.timeout_sec == 120.0
        assert c.max_retries == 3


class TestAgentConfig:
    def test_defaults(self):
        c = AgentConfig()
        assert c.base_url == "http://127.0.0.1:3030"
        assert c.auto_approve_all is False
        assert c.timeout_sec == 120.0
