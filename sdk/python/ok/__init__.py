"""
OK Agent Python SDK.

Connect to a running OK agent instance via HTTP/SSE, send prompts,
and stream typed events (text, tool calls, usage, approvals, etc.).

Quick start::

    from ok import Agent

    agent = Agent()
    if agent.connect():
        result = agent.run("Hello!")
        print(result.text)
    else:
        print("Agent not running at http://127.0.0.1:3030")
        print("Start it with: ok serve")
"""

from .client import Agent, AgentConfig, RunResult
from .async_client import AsyncAgent, AsyncAgentTransport, AsyncTransportConfig
from .events import (
    Event,
    ToolCall,
    Usage,
    ApprovalRequest,
    AskOption,
    AskQuestion,
    AskState,
    TURN_STARTED,
    REASONING,
    TEXT,
    MESSAGE,
    TOOL_DISPATCH,
    TOOL_RESULT,
    USAGE,
    APPROVAL,
    ASK,
    ERROR_EVENT,
    DONE,
    CANCELLED,
)
from .transport import AgentTransport, TransportConfig, TransportError, EventStream
from .transport_ws import WebSocketTransport, WSConfig, WSEventStream

__all__ = [
    # High-level client
    "Agent",
    "AgentConfig",
    "RunResult",

    # Async client
    "AsyncAgent",
    "AsyncAgentTransport",
    "AsyncTransportConfig",

    # Transport
    "AgentTransport",
    "TransportConfig",
    "TransportError",
    "EventStream",
    "WebSocketTransport",
    "WSConfig",
    "WSEventStream",

    # Event types
    "Event",
    "ToolCall",
    "Usage",
    "ApprovalRequest",
    "AskOption",
    "AskQuestion",
    "AskState",

    # Event kind constants
    "TURN_STARTED",
    "REASONING",
    "TEXT",
    "MESSAGE",
    "TOOL_DISPATCH",
    "TOOL_RESULT",
    "USAGE",
    "APPROVAL",
    "ASK",
    "ERROR_EVENT",
    "DONE",
    "CANCELLED",
]

__version__ = "0.1.0"
