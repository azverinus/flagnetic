"""The ingest client: the envelope of POST /sdk/v1/events, its batches and what the server answered.

It holds no queue and no timers: what to send, and when to send it again,
belongs to the reporter, which sends the same events with the same event IDs
so that a retry is dropped as a duplicate on receipt.
"""

from __future__ import annotations

import contextlib
import email.utils
import gzip
import http.client
import json
import os
import socket
import ssl
import threading
import time
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from datetime import UTC, datetime
from enum import Enum
from typing import Any
from urllib.parse import urlsplit

#: This SDK's name in the envelope and in the SDK inventory, as the User-Agent names it.
SDK_NAME = "flagnetic-python"

# Limits of POST /sdk/v1/events. The body limit is on the decompressed envelope, so a batch is
# packed against it before it is gzipped.
MAX_EVENTS_PER_BATCH = 500
MAX_SUMMARY_ROWS = 1000
MAX_BATCH_BYTES = 1 << 20
# How much of a 202 answer is read before it is given up on.
_MAX_ANSWER_BYTES = 1 << 20

#: One event in the shape the events endpoint takes.
Event = Mapping[str, Any]


class Outcome(Enum):
    """What the server did with a batch, and whether it may be sent again.

    ``ACCEPTED``: 202; the batch is done, its rejected events included, since a
    rejection is final. ``RETRY``: 429, 5xx or a network error; the same events,
    with the same IDs, go again after a backoff. ``UNAUTHORIZED``: 401 or 403;
    retried too, at the refused key's cap, since only an operator restoring the
    key ends it. ``DROPPED``: any other 4xx, which would fail the same way again.
    """

    ACCEPTED = "accepted"
    RETRY = "retry"
    UNAUTHORIZED = "unauthorized"
    DROPPED = "dropped"


@dataclass(frozen=True, slots=True)
class Sent:
    """The outcome of one attempt at one batch."""

    outcome: Outcome
    status: int = 0
    cause: str = ""
    #: Seconds the server's Retry-After asked for, when it sent one.
    retry_after: float | None = None
    accepted: int = 0
    duplicates: int = 0
    dropped_rows: int = 0
    #: The codes of the events the server rejected for good.
    rejected: list[str] = field(default_factory=list)


def iso(at: datetime) -> str:
    """An RFC 3339 timestamp in UTC with milliseconds."""
    return at.astimezone(UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def event_id(at: datetime) -> str:
    """A lowercase UUIDv7 whose first 48 bits are ``at`` in Unix milliseconds.

    The server reads the event's time from the ID itself and drops a retry as a
    duplicate, so the ID is chosen once, when the event is recorded.
    """
    ms = max(int(at.timestamp() * 1000), 0)
    raw = bytearray(ms.to_bytes(6, "big") + os.urandom(10))
    raw[6] = (raw[6] & 0x0F) | 0x70
    raw[8] = (raw[8] & 0x3F) | 0x80
    text = raw.hex()
    return f"{text[:8]}-{text[8:12]}-{text[12:16]}-{text[16:20]}-{text[20:]}"


def _size(event: Event) -> int:
    return len(json.dumps(event, separators=(",", ":"), ensure_ascii=False).encode())


class Ingest:
    """Posts batches of events; it never retries, the caller decides that from the outcome."""

    def __init__(
        self,
        *,
        base_url: str,
        sdk_key: str,
        agent: str,
        platform: str,
        version: str,
        schema_version: int,
        request_timeout: float,
        ssl_context: ssl.SSLContext | None = None,
    ) -> None:
        parts = urlsplit(base_url)
        self._secure = parts.scheme == "https"
        self._host = parts.hostname or ""
        self._port = parts.port
        self._path = f"{parts.path.rstrip('/')}/sdk/v1/events"
        self._sdk_key = sdk_key
        self._agent = agent
        self._identity = {"name": SDK_NAME, "version": version, "platform": platform}
        self._schema_version = schema_version
        self._timeout = request_timeout
        self._ssl_context = ssl_context
        self._lock = threading.Lock()
        self._conn: http.client.HTTPConnection | None = None

    def pack(self, events: Sequence[Event]) -> list[list[Event]]:
        """Splits events into batches the endpoint takes: at most 500 and, decompressed, under 1 MiB.

        An event too large on its own is still a batch of one, which the server
        answers 413 and the caller drops.
        """
        budget = MAX_BATCH_BYTES - len(self._envelope([]))
        batches: list[list[Event]] = []
        current: list[Event] = []
        size = 0
        for event in events:
            # One comma per event after the first.
            cost = _size(event) + 1
            if current and (len(current) == MAX_EVENTS_PER_BATCH or size + cost > budget):
                batches.append(current)
                current, size = [], 0
            current.append(event)
            size += cost
        if current:
            batches.append(current)
        return batches

    def post(self, events: Sequence[Event]) -> Sent:
        """Sends one batch and reports what the server answered."""
        body = gzip.compress(self._envelope(events), mtime=0)
        headers = {
            "Authorization": f"Bearer {self._sdk_key}",
            "Content-Type": "application/json",
            "Content-Encoding": "gzip",
            "User-Agent": self._agent,
            "Flagnetic-Schema-Version": str(self._schema_version),
        }
        if self._secure:
            conn: http.client.HTTPConnection = http.client.HTTPSConnection(
                self._host, self._port, timeout=self._timeout, context=self._ssl_context
            )
        else:
            conn = http.client.HTTPConnection(self._host, self._port, timeout=self._timeout)
        with self._lock:
            self._conn = conn
        deadline = time.monotonic() + self._timeout
        try:
            conn.request("POST", self._path, body=body, headers=headers)
            resp = conn.getresponse()
            status = resp.status
            if status == 202:
                return self._accepted(resp, deadline)
            retry_after = _retry_after(resp.getheader("Retry-After"))
        except TimeoutError as err:
            return Sent(Outcome.RETRY, cause=f"timeout: {err}")
        except (OSError, http.client.HTTPException) as err:
            return Sent(Outcome.RETRY, cause=f"network: {err or type(err).__name__}")
        finally:
            with self._lock:
                self._conn = None
            conn.close()
        cause = f"status {status}"
        if status in (401, 403):
            return Sent(Outcome.UNAUTHORIZED, status, cause)
        if status == 429 or status >= 500:
            return Sent(Outcome.RETRY, status, cause, retry_after)
        return Sent(Outcome.DROPPED, status, cause)

    def abort(self) -> None:
        """Ends a request in flight now rather than at its timeout."""
        with self._lock:
            sock = getattr(self._conn, "sock", None)
        if sock is not None:
            with contextlib.suppress(OSError):
                sock.shutdown(socket.SHUT_RDWR)

    def _envelope(self, events: Sequence[Event]) -> bytes:
        return json.dumps(
            {
                "schema_version": self._schema_version,
                "sdk": self._identity,
                "sent_at": iso(datetime.now(UTC)),
                "events": list(events),
            },
            separators=(",", ":"),
            ensure_ascii=False,
        ).encode()

    @staticmethod
    def _accepted(resp: http.client.HTTPResponse, deadline: float) -> Sent:
        """The 202 answer. One that does not decode leaves the counts zero: the batch was taken either way."""
        parts: list[bytes] = []
        size = 0
        while size <= _MAX_ANSWER_BYTES and time.monotonic() < deadline:
            piece = resp.read(1 << 16)
            if not piece:
                break
            parts.append(piece)
            size += len(piece)
        try:
            answer = json.loads(b"".join(parts))
        except ValueError:
            return Sent(Outcome.ACCEPTED, 202)
        if not isinstance(answer, dict):
            return Sent(Outcome.ACCEPTED, 202)
        rejected = answer.get("rejected")
        return Sent(
            Outcome.ACCEPTED,
            202,
            accepted=_count(answer.get("accepted")),
            duplicates=_count(answer.get("duplicates")),
            dropped_rows=_count(answer.get("dropped_rows")),
            rejected=[str(r.get("code", "")) for r in rejected if isinstance(r, dict)]
            if isinstance(rejected, list)
            else [],
        )


def _count(value: Any) -> int:
    return value if type(value) is int else 0


def _retry_after(value: str | None) -> float | None:
    """Retry-After in seconds, from either of its forms; None when absent or unreadable."""
    if not value:
        return None
    value = value.strip()
    if value.isdigit():
        return float(value)
    try:
        when = email.utils.parsedate_to_datetime(value)
    except (TypeError, ValueError):
        return None
    if when.tzinfo is None:
        when = when.replace(tzinfo=UTC)
    return max((when - datetime.now(UTC)).total_seconds(), 0.0)
