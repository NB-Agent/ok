"""
Async OK Agent client using ``httpx.AsyncClient`` for full async/await support.

Usage::

    import asyncio
    from ok import AsyncAgent

    async def main():
        agent = AsyncAgent()
        if await agent.connect():
            result = await agent.run("Hello!")
            print(result.text)

    asyncio.run(main())
"""

from __future__ import annotations

import asyncio
import json
import time
from dataclasses import dataclass
from typing import Any, AsyncGenerator, Optional

import httpx

from .events import (
    Event,
    DONE,
    CANCELLED,
    ERROR_EVENT,
    APPROVAL,
    ASK,
    TEXT,
    MESSAGE,
    TOOL_DISPATCH,
    USAGE,
)
from .transport import TransportConfig, TransportError
from .client import AgentConfig, RunResult


# ── Async Transport ────────────────────────────────────────────────────────

@dataclass
class AsyncTransportConfig:
    """Configuration for the async HTTP/SSE transport."""
    base_url: str = "http://127.0.0.1:3030"
    timeout_sec: float = 120.0
    connect_timeout_sec: float = 10.0
    max_retries: int = 3
    retry_delay_sec: float = 1.0


class AsyncAgentTransport:
    """Async HTTP/SSE transport for the OK Agent protocol.

    Uses ``httpx.AsyncClient`` for non-blocking I/O. Suitable for
    asyncio applications, FastAPI servers, and Jupyter notebooks.
    """

    def __init__(self, config: Optional[AsyncTransportConfig] = None) -> None:
        self.config = config or AsyncTransportConfig()
        self._client: Optional[httpx.AsyncClient] = None
        self._base = self.config.base_url.rstrip("/")

    @property
    def client(self) -> httpx.AsyncClient:
        if self._client is None:
            self._client = httpx.AsyncClient(
                timeout=httpx.Timeout(
                    self.config.timeout_sec,
                    connect=self.config.connect_timeout_sec,
                ),
                headers={"User-Agent": "ok-sdk-python/0.1.0"},
            )
        return self._client

    async def close(self) -> None:
        """Close the underlying HTTP client."""
        if self._client is not None:
            await self._client.aclose()
            self._client = None

    async def __aenter__(self) -> AsyncAgentTransport:
        return self

    async def __aexit__(self, *args: Any) -> None:
        await self.close()

    # ── Health ────────────────────────────────────────────────────────

    async def health(self) -> bool:
        """Check if the agent is reachable via ``/healthz``."""
        try:
            resp = await self.client.get(f"{self._base}/healthz")
            return resp.status_code == 200
        except Exception:
            return False

    # ── Session management ────────────────────────────────────────────

    async def new_session(self) -> None:
        """Start a new conversation (clears history)."""
        await self.client.post(f"{self._base}/new")

    async def history(self) -> list[dict[str, str]]:
        """Return conversation history."""
        resp = await self.client.get(f"{self._base}/history")
        resp.raise_for_status()
        return resp.json()

    async def context(self) -> dict[str, int]:
        """Return context window usage."""
        resp = await self.client.get(f"{self._base}/context")
        resp.raise_for_status()
        return resp.json()

    async def compact(self) -> None:
        """Compact the session."""
        await self.client.post(f"{self._base}/compact")

    # ── Submit & control ──────────────────────────────────────────────

    async def submit(self, text: str) -> None:
        """Submit text to the agent (non-blocking)."""
        resp = await self.client.post(
            f"{self._base}/submit",
            json={"text": text},
        )
        resp.raise_for_status()

    async def cancel(self) -> None:
        """Cancel the current turn."""
        await self.client.post(f"{self._base}/cancel")

    async def set_plan_mode(self, on: bool) -> None:
        """Enable or disable plan mode."""
        await self.client.post(
            f"{self._base}/plan",
            json={"on": on},
        )

    async def approve(self, approval_id: str, allow: bool = True) -> None:
        """Approve or reject a tool call."""
        await self.client.post(
            f"{self._base}/approve",
            json={"id": approval_id, "allow": allow},
        )

    # ── SSE event stream ──────────────────────────────────────────────

    async def stream(
        self,
        auto_reconnect: bool = True,
    ) -> AsyncGenerator[Event, None]:
        """Open an SSE event stream and yield events.

        Args:
            auto_reconnect: If True, reconnect on connection loss.

        Yields:
            ``Event`` objects from the agent's SSE stream.
        """
        attempt = 0
        while True:
            attempt += 1
            try:
                async with self.client.stream("GET", f"{self._base}/events") as resp:
                    resp.raise_for_status()
                    attempt = 0  # reset on successful connect
                    buffer = ""
                    async for chunk in resp.aiter_text():
                        buffer += chunk
                        while "\n\n" in buffer:
                            event_str, buffer = buffer.split("\n\n", 1)
                            event = self._parse_sse(event_str)
                            if event is not None:
                                yield event
            except httpx.HTTPError:
                if not auto_reconnect or attempt >= self.config.max_retries:
                    yield Event(kind=ERROR_EVENT, err="transport: connection lost")
                    if auto_reconnect:
                        raise TransportError(
                            f"Connection lost after {attempt} attempt(s)"
                        )
                    return
                await asyncio.sleep(self.config.retry_delay_sec)

    @staticmethod
    def _parse_sse(raw: str) -> Optional[Event]:
        """Parse an SSE event block into an Event object."""
        data = ""
        for line in raw.split("\n"):
            if line.startswith("data: "):
                data += line[6:]
        if not data:
            return None
        try:
            payload = json.loads(data)
            return Event.from_json(json.dumps(payload))
        except (json.JSONDecodeError, TypeError):
            return None


# ── Async Agent ────────────────────────────────────────────────────────────

class AsyncAgent:
    """High-level async OK Agent client.

    Connect to a running OK agent serve endpoint and interact with it
    using async/await.

    Usage::

        import asyncio
        from ok import AsyncAgent

        async def main():
            agent = AsyncAgent()
            result = await agent.run("What is the weather?")
            print(result.text)

        asyncio.run(main())
    """

    def __init__(
        self,
        config: Optional[AgentConfig] = None,
        transport: Optional[AsyncAgentTransport] = None,
    ) -> None:
        self.config = config or AgentConfig()
        self._transport = transport or AsyncAgentTransport(
            AsyncTransportConfig(
                base_url=self.config.base_url,
                timeout_sec=self.config.timeout_sec,
                max_retries=self.config.max_retries,
            )
        )

    # ── Connection ──────────────────────────────────────────────────────

    async def connect(self) -> bool:
        """Check if the agent is reachable via ``/healthz``."""
        return await self._transport.health()

    async def close(self) -> None:
        """Close the underlying HTTP connection."""
        await self._transport.close()

    async def __aenter__(self) -> AsyncAgent:
        return self

    async def __aexit__(self, *args: Any) -> None:
        await self.close()

    # ── Session management ──────────────────────────────────────────────

    async def new_session(self) -> None:
        """Start a new conversation."""
        await self._transport.new_session()

    async def get_history(self) -> list[dict[str, str]]:
        """Return conversation history."""
        return await self._transport.history()

    async def get_context(self) -> dict[str, int]:
        """Return context window usage."""
        return await self._transport.context()

    async def compact(self) -> None:
        """Compact the session."""
        await self._transport.compact()

    # ── Submit & control ────────────────────────────────────────────────

    async def submit(self, text: str) -> None:
        """Submit text to the agent (non-blocking)."""
        await self._transport.submit(text)

    async def cancel(self) -> None:
        """Cancel the current turn."""
        await self._transport.cancel()

    async def set_plan_mode(self, on: bool) -> None:
        """Enable or disable plan mode."""
        await self._transport.set_plan_mode(on)

    # ── Event stream ────────────────────────────────────────────────────

    async def stream(
        self,
        auto_approve: bool = False,
    ) -> AsyncGenerator[Event, None]:
        """Open an SSE event stream and yield events.

        Args:
            auto_approve: If True, auto-approve tool approval requests.

        Yields:
            ``Event`` objects from the agent's SSE stream.
        """
        async for event in self._transport.stream(auto_reconnect=self.config.auto_reconnect):
            if auto_approve and event.kind == APPROVAL and event.approval:
                await self._transport.approve(event.approval.id, allow=True)
            yield event

    # ── High-level convenience ──────────────────────────────────────────

    async def run(
        self,
        text: str,
        auto_approve: Optional[bool] = None,
        timeout_sec: Optional[float] = None,
    ) -> RunResult:
        """Submit text and collect all events into a ``RunResult``.

        Args:
            text: User input to send.
            auto_approve: Auto-approve tool calls (default uses config).
            timeout_sec: Max seconds to wait.

        Returns:
            A ``RunResult`` with aggregated text, tools, and usage.
        """
        result = RunResult()
        auto_ok = auto_approve if auto_approve is not None else self.config.auto_approve_all

        await self.submit(text)

        deadline = time.monotonic() + (timeout_sec or self.config.timeout_sec)

        async for event in self.stream(auto_approve=auto_ok):
            if time.monotonic() > deadline:
                await self.cancel()
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

    async def ask_question(
        self,
        text: str,
        auto_approve: bool = False,
    ) -> tuple[RunResult, list[Event]]:
        """Submit text and return unhandled ask/approval events.

        Returns:
            (RunResult, list of unhandled approval/ask Events)
        """
        result = RunResult()
        pending: list[Event] = []

        await self.submit(text)
        async for event in self.stream(auto_approve=auto_approve):
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
