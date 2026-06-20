"""
HTTP/SSE transport for connecting to an OK agent serve endpoint.

Handles:
- POST /submit — send user input
- GET /events — receive streaming SSE events
- POST /cancel — cancel current turn
- POST /approve — approve/reject tool calls
- GET /history — fetch conversation history
"""

from __future__ import annotations

import json
import time
from dataclasses import dataclass, field
from typing import Any, Callable, Optional
from urllib.parse import urljoin

import httpx

from .events import Event, DONE, CANCELLED, ERROR_EVENT, APPROVAL, ASK

# Type alias for event callbacks
EventHandler = Callable[[Event], None]


@dataclass
class TransportConfig:
    """Configuration for the HTTP/SSE transport."""
    base_url: str = "http://127.0.0.1:3030"
    timeout_sec: float = 120.0
    connect_timeout_sec: float = 10.0
    max_retries: int = 3
    retry_delay_sec: float = 1.0


class TransportError(Exception):
    """Raised when the transport layer encounters a non-recoverable error."""


class AgentTransport:
    """Low-level HTTP/SSE transport for the OK Agent protocol.

    This class handles the raw HTTP communication with an OK agent's
    serve endpoint. Most users should use the higher-level ``Agent``
    class in ``client.py`` instead.
    """

    def __init__(self, config: Optional[TransportConfig] = None) -> None:
        self.config = config or TransportConfig()
        self._client: Optional[httpx.Client] = None
        self._base = self.config.base_url.rstrip("/")

    # ── Connection lifecycle ────────────────────────────────────────────

    @property
    def client(self) -> httpx.Client:
        if self._client is None:
            self._client = httpx.Client(
                timeout=httpx.Timeout(
                    self.config.timeout_sec,
                    connect=self.config.connect_timeout_sec,
                ),
                headers={"User-Agent": "ok-sdk-python/0.1.0"},
            )
        return self._client

    def close(self) -> None:
        """Close the underlying HTTP client."""
        if self._client is not None:
            self._client.close()
            self._client = None

    def __enter__(self) -> AgentTransport:
        return self

    def __exit__(self, *args: Any) -> None:
        self.close()

    # ── Core API calls ──────────────────────────────────────────────────

    def health(self) -> bool:
        """Check if the agent is alive."""
        try:
            resp = self.client.get(f"{self._base}/healthz")
            return resp.status_code == 200
        except httpx.RequestError:
            return False

    def submit(self, text: str) -> None:
        """Submit user input to the agent. Returns immediately.

        The agent processes asynchronously; listen to ``stream()`` for results.
        """
        resp = self.client.post(
            f"{self._base}/submit",
            json={"input": text},
        )
        if resp.status_code != 202:
            raise TransportError(
                f"submit failed: HTTP {resp.status_code} {resp.text[:200]}"
            )

    def cancel(self) -> None:
        """Cancel the current turn."""
        try:
            self.client.post(f"{self._base}/cancel")
        except httpx.RequestError:
            pass  # best-effort

    def approve(self, approval_id: str, allow: bool = True, session: bool = False) -> None:
        """Approve or reject a tool call."""
        resp = self.client.post(
            f"{self._base}/approve",
            json={"id": approval_id, "allow": allow, "session": session},
        )
        if resp.status_code != 204:
            raise TransportError(
                f"approve failed: HTTP {resp.status_code} {resp.text[:200]}"
            )

    def set_plan_mode(self, on: bool) -> None:
        """Enable or disable plan mode."""
        resp = self.client.post(
            f"{self._base}/plan",
            json={"on": on},
        )
        if resp.status_code != 204:
            raise TransportError(
                f"plan mode failed: HTTP {resp.status_code} {resp.text[:200]}"
            )

    def new_session(self) -> None:
        """Start a new conversation session."""
        resp = self.client.post(f"{self._base}/new")
        if resp.status_code != 204:
            raise TransportError(
                f"new session failed: HTTP {resp.status_code} {resp.text[:200]}"
            )

    def compact(self) -> None:
        """Compact the current session (summarise history to save tokens)."""
        resp = self.client.post(f"{self._base}/compact")
        if resp.status_code not in (204, 409):
            raise TransportError(
                f"compact failed: HTTP {resp.status_code} {resp.text[:200]}"
            )

    def history(self) -> list[dict[str, str]]:
        """Return the conversation history as a list of {role, content} dicts."""
        resp = self.client.get(f"{self._base}/history")
        if resp.status_code != 200:
            raise TransportError(
                f"history failed: HTTP {resp.status_code} {resp.text[:200]}"
            )
        return resp.json()

    def context(self) -> dict[str, int]:
        """Return context usage info: {used, window} tokens."""
        resp = self.client.get(f"{self._base}/context")
        if resp.status_code != 200:
            raise TransportError(
                f"context failed: HTTP {resp.status_code} {resp.text[:200]}"
            )
        return resp.json()

    # ── SSE Event Stream ────────────────────────────────────────────────

    def stream(
        self,
        on_event: Optional[EventHandler] = None,
        auto_reconnect: bool = True,
    ) -> EventStream:
        """Open an SSE event stream from the agent.

        Args:
            on_event: Optional callback fired on each event.
            auto_reconnect: If True, re-connect on drop (up to max_retries).

        Returns:
            An ``EventStream`` context manager. Iterate over it to receive
            ``Event`` objects.

        Usage::

            with transport.stream() as events:
                for event in events:
                    if event.kind == Event.TEXT:
                        print(event.text, end="")
        """
        return EventStream(self, on_event, auto_reconnect)


class EventStream:
    """An SSE event stream from the OK agent.

    Created by ``AgentTransport.stream()``. Use as a context manager
    and iterate to receive ``Event`` objects::

        with transport.stream() as stream:
            for event in stream:
                print(event.kind, event.text)
    """

    def __init__(
        self,
        transport: AgentTransport,
        on_event: Optional[EventHandler] = None,
        auto_reconnect: bool = True,
    ) -> None:
        self._transport = transport
        self._on_event = on_event
        self._auto_reconnect = auto_reconnect
        self._response: Optional[httpx.Response] = None

    def __enter__(self) -> EventStream:
        return self

    def __exit__(self, *args: Any) -> None:
        if self._response is not None:
            self._response.close()

    def __iter__(self):
        """Yield Event objects from the SSE stream."""
        retries = 0
        max_retries = self._transport.config.max_retries if self._auto_reconnect else 0

        while retries <= max_retries:
            try:
                url = f"{self._transport._base}/events"
                with self._transport.client.stream("GET", url) as response:
                    self._response = response
                    if response.status_code != 200:
                        raise TransportError(
                            f"SSE stream failed: HTTP {response.status_code}"
                        )

                    for line in response.iter_lines():
                        line = line.strip()
                        if not line or line.startswith(":") or line.startswith("id:"):
                            # comment / heartbeat / id line — skip
                            continue
                        if line.startswith("data: "):
                            raw = line[6:]
                            event = Event.from_json(raw)
                            if self._on_event:
                                self._on_event(event)
                            yield event

                    # Stream ended cleanly
                    break

            except (httpx.RequestError, TransportError) as exc:
                retries += 1
                if retries > max_retries:
                    raise TransportError(
                        f"SSE stream lost after {retries} retries: {exc}"
                    ) from exc
                time.sleep(self._transport.config.retry_delay_sec * retries)
