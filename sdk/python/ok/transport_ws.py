"""
WebSocket transport for the HCP (Human Communication Protocol).

Connects to ``ws://host:port/ws`` and exchanges JSON messages bidirectionally.
Faster and more responsive than HTTP/SSE for interactive use — commands and
events share one persistent connection with no HTTP overhead.

Usage::

    from ok import Agent
    from ok.transport_ws import WebSocketTransport

    transport = WebSocketTransport("ws://127.0.0.1:3030/ws")
    agent = Agent(transport=transport)

    result = agent.run("Hello!")
    print(result.text)
"""

from __future__ import annotations

import json
import queue
import threading
import time
from dataclasses import dataclass, field
from typing import Any, Callable, Generator, Optional

from .events import Event, DONE, CANCELLED, ERROR_EVENT, APPROVAL, ASK
from .transport import EventHandler, TransportError

# Try to import websocket-client; raise a helpful message if missing.
try:
    import websocket  # noqa: F401
except ImportError:
    raise ImportError(
        "WebSocket transport requires the 'websocket-client' library.\n"
        "Install it:  pip install websocket-client"
    )


@dataclass
class WSConfig:
    """Configuration for the HCP WebSocket transport."""
    url: str = "ws://127.0.0.1:3030/ws"
    timeout_sec: float = 120.0
    ping_interval_sec: float = 30.0
    max_retries: int = 3
    retry_delay_sec: float = 1.0


class WebSocketTransport:
    """HCP WebSocket transport — bidirectional JSON messaging with OK Agent.

    One persistent connection carries both commands (client→server) and
    events (server→client). This is more efficient than HTTP/SSE for
    interactive use.
    """

    def __init__(self, config: Optional[WSConfig] = None) -> None:
        self.config = config or WSConfig()
        self._ws: Optional["websocket.WebSocket"] = None
        self._lock = threading.Lock()
        self._connected = False
        self._event_queue: queue.Queue = queue.Queue(maxsize=256)
        self._reader_thread: Optional[threading.Thread] = None
        self._stop_event = threading.Event()

    # ── Connection lifecycle ────────────────────────────────────────────

    def connect(self) -> bool:
        """Connect to the WebSocket endpoint.

        Returns True on success. Thread-safe.
        """
        with self._lock:
            if self._connected:
                return True
            try:
                import websocket as ws
                self._ws = ws.create_connection(
                    self.config.url,
                    timeout=self.config.timeout_sec,
                    enable_multithread=True,
                )
                self._connected = True
                self._stop_event.clear()

                # Start background reader thread.
                self._reader_thread = threading.Thread(
                    target=self._reader_loop,
                    daemon=True,
                    name="ok-ws-reader",
                )
                self._reader_thread.start()
                return True
            except Exception:
                self._connected = False
                return False

    def close(self) -> None:
        """Close the WebSocket connection."""
        self._stop_event.set()
        with self._lock:
            if self._ws is not None:
                try:
                    self._ws.close()
                except Exception:
                    pass
                self._ws = None
                self._connected = False

    def __enter__(self) -> WebSocketTransport:
        return self

    def __exit__(self, *args: Any) -> None:
        self.close()

    @property
    def connected(self) -> bool:
        return self._connected

    # ── Reader loop (runs in background thread) ─────────────────────────

    def _reader_loop(self) -> None:
        """Read JSON frames from the WebSocket and enqueue them."""
        import websocket as ws

        while not self._stop_event.is_set():
            try:
                if self._ws is None:
                    break
                # Use a timeout so we can check stop_event periodically.
                self._ws.settimeout(1.0)
                raw = self._ws.recv()
                if raw is None:
                    continue
                if isinstance(raw, bytes):
                    raw = raw.decode("utf-8")
                self._event_queue.put(raw)
            except ws.WebSocketTimeoutException:
                continue
            except Exception:
                if not self._stop_event.is_set():
                    # Connection lost — signal end of stream.
                    self._connected = False
                break

    # ── Send commands ───────────────────────────────────────────────────

    def _send(self, data: dict[str, Any]) -> None:
        """Send a JSON command to the server.

        Reconnects automatically if the connection is lost.
        """
        with self._lock:
            if self._ws is None or not self._connected:
                raise TransportError("WebSocket not connected")

            try:
                self._ws.send(json.dumps(data))
            except Exception as exc:
                self._connected = False
                raise TransportError(f"send failed: {exc}") from exc

    # ── Core API (same interface as AgentTransport) ─────────────────────

    def health(self) -> bool:
        """Check if the WebSocket is connected and alive."""
        if not self._connected:
            return self.connect()
        return True

    def submit(self, text: str) -> None:
        """Submit user input to the agent."""
        self._send({"type": "submit", "input": text})

    def cancel(self) -> None:
        """Cancel the current turn."""
        try:
            self._send({"type": "cancel"})
        except TransportError:
            pass  # best-effort

    def approve(self, approval_id: str, allow: bool = True, session: bool = False) -> None:
        """Approve or reject a tool call."""
        self._send({
            "type": "approve",
            "id": approval_id,
            "allow": allow,
            "session": session,
        })

    def set_plan_mode(self, on: bool) -> None:
        """Enable or disable plan mode."""
        self._send({"type": "plan", "on": on})

    def new_session(self) -> None:
        """Start a new conversation session."""
        self._send({"type": "new_session"})

    def compact(self) -> None:
        """Compact the current session."""
        self._send({"type": "compact"})

    def history(self) -> list[dict[str, str]]:
        """Fetch conversation history.

        Sends a ``history`` command and waits for the response event.
        """
        # Drain any pending events first.
        self._drain_queue()
        self._send({"type": "history"})
        timeout = time.monotonic() + 10.0
        while time.monotonic() < timeout:
            try:
                raw = self._event_queue.get(timeout=1.0)
                data = json.loads(raw)
                if isinstance(data, dict) and data.get("kind") == "history":
                    return data.get("messages", [])
            except (queue.Empty, json.JSONDecodeError):
                continue
        raise TransportError("history: no response within 10s")

    def context(self) -> dict[str, int]:
        """Fetch context usage info."""
        self._drain_queue()
        self._send({"type": "context"})
        timeout = time.monotonic() + 10.0
        while time.monotonic() < timeout:
            try:
                raw = self._event_queue.get(timeout=1.0)
                data = json.loads(raw)
                if isinstance(data, dict) and data.get("kind") == "context_response":
                    return {"used": data.get("used", 0), "window": data.get("window", 0)}
            except (queue.Empty, json.JSONDecodeError):
                continue
        raise TransportError("context: no response within 10s")

    def _drain_queue(self) -> None:
        """Drain non-response events so history/context get a clean read."""
        while not self._event_queue.empty():
            try:
                self._event_queue.get_nowait()
            except queue.Empty:
                break

    # ── Event stream ────────────────────────────────────────────────────

    def stream(
        self,
        on_event: Optional[EventHandler] = None,
        auto_reconnect: bool = True,
    ) -> WSEventStream:
        """Open the event stream from the WebSocket connection.

        Unlike HTTP/SSE which needs a separate connection, WebSocket events
        arrive on the same connection — this just starts reading from the
        shared event queue.
        """
        return WSEventStream(self, on_event, auto_reconnect)


class WSEventStream:
    """Event stream over a WebSocket connection.

    Created by ``WebSocketTransport.stream()``. Iterate to receive events.
    """

    def __init__(
        self,
        transport: WebSocketTransport,
        on_event: Optional[EventHandler] = None,
        auto_reconnect: bool = True,
    ) -> None:
        self._transport = transport
        self._on_event = on_event
        self._auto_reconnect = auto_reconnect

    def __enter__(self) -> WSEventStream:
        return self

    def __exit__(self, *args: Any) -> None:
        pass

    def __iter__(self) -> Generator[Event, None, None]:
        retries = 0
        max_retries = self._transport.config.max_retries if self._auto_reconnect else 0

        while retries <= max_retries:
            try:
                # Ensure connected.
                if not self._transport.connected:
                    connected = self._transport.connect()
                    if not connected:
                        raise TransportError("WebSocket connection failed")
                    retries = 0  # reset retries on successful connect

                # Read events from the queue.
                while not self._transport._stop_event.is_set():
                    try:
                        raw = self._transport._event_queue.get(timeout=1.0)
                    except queue.Empty:
                        # Check if connection is still alive.
                        if not self._transport.connected and not self._transport._stop_event.is_set():
                            raise TransportError("WebSocket connection lost")
                        continue

                    event = Event.from_json(raw)
                    if self._on_event:
                        self._on_event(event)
                    yield event

                break  # stop_event set — normal shutdown

            except TransportError as exc:
                retries += 1
                if retries > max_retries:
                    return  # exhausted retries; generator ends
                time.sleep(self._transport.config.retry_delay_sec * retries)
