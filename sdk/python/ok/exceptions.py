"""Custom exceptions for the OK Agent Python SDK."""


class OKError(Exception):
    """Base exception for all OK SDK errors."""


class ConnectionError(OKError):
    """Raised when the agent is unreachable."""


class TransportError(OKError):
    """Raised when an HTTP/SSE transport call fails."""


class TimeoutError(OKError):
    """Raised when an agent call exceeds the timeout."""


class ApprovalRequired(OKError):
    """Raised when a tool call requires approval and no handler is set."""
