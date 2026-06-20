"""
High-level OK Agent client.

The ``Agent`` class wraps the HTTP/SSE transport into a simple interface::

    from ok import Agent

    agent = Agent()

    # Simple synchronous call
    for event in agent.run("Hello, who are you?"):
        if event.kind == event.TEXT:
            print(event.text, end="")

    # Process events with automatic approval
    for event in agent.run("List my files", auto_approve=True):
        if event.kind == event.TEXT:
            print(event.text, end="")
        elif event.kind == event.USAGE:
            print(f"[tokens: {event.usage.total_tokens}]")
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Generator, Optional

from .events import (
    Event,
    DONE,
    CANCELLED,
    ERROR_EVENT,
    APPROVAL,
    ASK,
    TEXT,
    REASONING,
    USAGE,
    TOOL_DISPATCH,
    TOOL_RESULT,
    MESSAGE,
    TURN_STARTED,
)
from .transport import AgentTransport, TransportConfig, TransportError


@dataclass
class AgentConfig:
    """Configuration for the Agent client.

    All parameters are optional. By default, connects to
    ``http://127.0.0.1:3030`` — the default OK serve address.
    """
    base_url: str = "http://127.0.0.1:3030"
    timeout_sec: float = 120.0
    auto_reconnect: bool = True
    max_retries: int = 3
    auto_approve_all: bool = False
    """If True, automatically approve every tool approval request."""


class RunResult:
    """The aggregated result of an ``agent.run()`` call.

    Collects all text output, final message, tool calls, and usage stats
    from a single turn.
    """

    def __init__(self) -> None:
        self.text_parts: list[str] = []
        self.final_message: str = ""
        self.tool_calls: list[dict[str, Any]] = []
        self.usage: Optional[dict[str, Any]] = None
        self.cancelled: bool = False
        self.error: str = ""

    @property
    def text(self) -> str:
        """All text output concatenated."""
        return "".join(self.text_parts)

    def __repr__(self) -> str:
        parts = []
        if self.text:
            parts.append(f"text={self.text[:60]!r}...")
        if self.tool_calls:
            parts.append(f"tools={len(self.tool_calls)}")
        if self.usage:
            parts.append(f"tokens={self.usage.get('total_tokens', 0)}")
        if self.error:
            parts.append(f"error={self.error!r}")
        if self.cancelled:
            parts.append("cancelled=True")
        return f"<RunResult {' | '.join(parts)}>"


class Agent:
    """High-level OK Agent client.

    Connect to a running OK agent serve endpoint and interact with it.

    Usage::

        agent = Agent()
        result = agent.run_sync("What is the weather in Beijing?")
        print(result.text)
        print(f"Tokens: {result.usage}")
    """

    def __init__(
        self,
        config: Optional[AgentConfig] = None,
        transport: Optional[AgentTransport] = None,
    ) -> None:
        self.config = config or AgentConfig()
        self._transport = transport or AgentTransport(
            TransportConfig(
                base_url=self.config.base_url,
                timeout_sec=self.config.timeout_sec,
                max_retries=self.config.max_retries,
            )
        )

    # ── Connection ──────────────────────────────────────────────────────

    def connect(self) -> bool:
        """Check if the agent is reachable.

        Returns True if the agent responds to ``/healthz``.
        """
        return self._transport.health()

    def close(self) -> None:
        """Close the underlying HTTP connection."""
        self._transport.close()

    def __enter__(self) -> Agent:
        return self

    def __exit__(self, *args: Any) -> None:
        self.close()

    # ── Session management ──────────────────────────────────────────────

    def new_session(self) -> None:
        """Start a new conversation (clears history)."""
        self._transport.new_session()

    def get_history(self) -> list[dict[str, str]]:
        """Return conversation history as ``[{role, content}, ...]``."""
        return self._transport.history()

    def get_context(self) -> dict[str, int]:
        """Return context window usage as ``{used, window}``."""
        return self._transport.context()

    def compact(self) -> None:
        """Compact the session to save tokens."""
        self._transport.compact()

    # ── Submit & stream ─────────────────────────────────────────────────

    def submit(self, text: str) -> None:
        """Submit text to the agent (non-blocking)."""
        self._transport.submit(text)

    def cancel(self) -> None:
        """Cancel the current turn."""
        self._transport.cancel()

    def set_plan_mode(self, on: bool) -> None:
        """Enable or disable plan mode."""
        self._transport.set_plan_mode(on)

    # ── Event stream ────────────────────────────────────────────────────

    def stream(
        self,
        auto_approve: bool = False,
    ) -> Generator[Event, None, None]:
        """Open an SSE event stream and yield events.

        Args:
            auto_approve: If True, automatically approve any tool approval
                         requests without user intervention.

        Yields:
            ``Event`` objects from the agent's SSE stream.
        """
        for event in self._transport.stream(auto_reconnect=self.config.auto_reconnect):
            if auto_approve and event.kind == APPROVAL and event.approval:
                self._transport.approve(event.approval.id, allow=True)
            yield event

    # ── High-level convenience ──────────────────────────────────────────

    def run(
        self,
        text: str,
        auto_approve: Optional[bool] = None,
        timeout_sec: Optional[float] = None,
    ) -> RunResult:
        """Submit text and collect all events into a ``RunResult``.

        This is the simplest way to interact with the agent::

            result = agent.run("What is 2+2?")
            print(result.text)  # "2+2 = 4"

        Args:
            text: User input to send to the agent.
            auto_approve: If True, auto-approve tool calls (default uses
                         config.auto_approve_all).
            timeout_sec: Maximum seconds to wait for completion.

        Returns:
            A ``RunResult`` with aggregated text, tools, and usage.
        """
        result = RunResult()
        auto_ok = auto_approve if auto_approve is not None else self.config.auto_approve_all

        self.submit(text)

        import time
        deadline = time.monotonic() + (timeout_sec or self.config.timeout_sec)

        for event in self.stream(auto_approve=auto_ok):
            if time.monotonic() > deadline:
                self.cancel()
                result.error = "timeout"
                break

            if event.kind == TEXT:
                result.text_parts.append(event.text)
            elif event.kind == MESSAGE:
                result.final_message = event.text
            elif event.kind == TOOL_DISPATCH and event.tool:
                result.tool_calls.append({
                    "name": event.tool.name,
                    "args": event.tool.args,
                    "output": event.tool.output,
                    "err": event.tool.err,
                    "id": event.tool.id,
                })
            elif event.kind == USAGE and event.usage:
                result.usage = {
                    "prompt_tokens": event.usage.prompt_tokens,
                    "completion_tokens": event.usage.completion_tokens,
                    "total_tokens": event.usage.total_tokens,
                    "cache_hit_tokens": event.usage.cache_hit_tokens,
                    "cache_miss_tokens": event.usage.cache_miss_tokens,
                    "cost_usd": event.usage.cost_usd,
                }
            elif event.kind == CANCELLED:
                result.cancelled = True
                break
            elif event.kind == DONE:
                break
            elif event.kind == ERROR_EVENT:
                result.error = event.err
                break

        return result

    def run_sync(self, text: str, **kwargs: Any) -> RunResult:
        """Alias for ``run()`` — submit and wait for completion."""
        return self.run(text, **kwargs)

    def ask_question(
        self,
        text: str,
        auto_approve: bool = False,
    ) -> tuple[RunResult, list[Event]]:
        """Submit text and also return unhandled ask/approval events.

        Useful for interactive applications where you need to present
        approval requests or multiple-choice questions to the user.

        Returns:
            (RunResult, list of unhandled approval/ask Events)
        """
        result = RunResult()
        pending: list[Event] = []

        self.submit(text)
        for event in self.stream(auto_approve=auto_approve):
            if event.kind == TEXT:
                result.text_parts.append(event.text)
            elif event.kind == DONE:
                break
            elif event.kind in (APPROVAL, ASK):
                pending.append(event)
            elif event.kind == CANCELLED:
                result.cancelled = True
                break
            elif event.kind == ERROR_EVENT:
                result.error = event.err
                break

        return result, pending
