"""The delivery thread: fetches the ruleset, holds the ruleset stream, polls and backs off.

One daemon thread does it all, so a client costs one thread however many
flags it serves. The ruleset stream is read from its socket by the thread
itself, between fetches, with ``select`` waiting for whichever comes first:
bytes on the stream, the next fetch or reconnect due, the heartbeat deadline,
or a wake-up from ``close``.
"""

from __future__ import annotations

import contextlib
import http.client
import json
import logging
import random
import select
import socket
import ssl
import threading
import time
import zlib
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any, Protocol, cast
from urllib.parse import urlsplit

from . import _events
from ._sse import ChunkedDecoder, EventStreamParser, Frame, StreamError

API_PATH = "/sdk/v1"

# Defaults for server mode, in seconds.
DEFAULT_REQUEST_TIMEOUT = 10.0
DEFAULT_POLL_INTERVAL = 300.0
MIN_POLL_INTERVAL = 30.0
DEFAULT_DOWN_POLL_INTERVAL = 30.0
RETRY_BASE = 1.0
RETRY_CAP = 300.0
UNAUTHORIZED_CAP = 900.0
# The server sends a comment every 15 seconds, so three missed ones reconnect.
HEARTBEAT_TIMEOUT = 45.0
STREAM_RETRY_CAP = 30.0
# A server-side close is routine: the platform closes every stream after about an hour.
CLOSE_JITTER_CAP = 2.0
# How long a connection must hold for the reconnect backoff to start from the base again.
STABLE_FOR = 60.0
# The waits before re-fetching when the ruleset endpoint answered below the version an event
# announced, a replica still behind. After the last one the safety poll takes over.
CATCH_UP_DELAYS = (1.0, 2.0, 4.0)
# A decompressed ruleset document is bounded.
MAX_RULESET_BYTES = 256 << 20

RULESET_EVENT = "ruleset"

# Causes of fetch_failed and sse_dropped.
CAUSE_TIMEOUT = "timeout"
CAUSE_NETWORK = "network"
CAUSE_HTTP_STATUS = "http_status"
CAUSE_UNAUTHORIZED = "unauthorized"
CAUSE_PARSE = "parse"
CAUSE_SCHEMA_UNSUPPORTED = "schema_unsupported"
CAUSE_WRONG_SCOPE = "wrong_scope"
CAUSE_VERSION_DOWNGRADE = "version_downgrade"
CAUSE_SERVER_CLOSED = "server_closed"
CAUSE_SILENCE = "silence"
CAUSE_MALFORMED = "malformed"

_FETCH_OK = "ok"
_FETCH_FAILED = "failed"
_FETCH_UNAUTHORIZED = "unauthorized"
_FETCH_CLOSING = "closing"


@dataclass(frozen=True, slots=True)
class Waits:
    """Every wait of the thread, in seconds, scaled by one factor.

    A test divides them all by one factor so that a scenario on real time runs
    in a fraction of it; the scenario divides its own durations by the same one.
    """

    request_timeout: float
    poll: float
    down_poll: float
    retry_base: float
    retry_cap: float
    unauthorized_cap: float
    stream_retry_cap: float
    close_jitter_cap: float
    stable_for: float
    heartbeat: float
    catch_up: tuple[float, ...]

    @classmethod
    def of(cls, request_timeout: float, poll_interval: float | None, divisor: float = 1.0) -> Waits:
        poll = DEFAULT_POLL_INTERVAL if poll_interval is None else poll_interval
        return cls(
            request_timeout=request_timeout,
            poll=max(poll / divisor, MIN_POLL_INTERVAL / divisor),
            down_poll=DEFAULT_DOWN_POLL_INTERVAL / divisor,
            retry_base=RETRY_BASE / divisor,
            retry_cap=RETRY_CAP / divisor,
            unauthorized_cap=UNAUTHORIZED_CAP / divisor,
            stream_retry_cap=STREAM_RETRY_CAP / divisor,
            close_jitter_cap=CLOSE_JITTER_CAP / divisor,
            stable_for=STABLE_FOR / divisor,
            heartbeat=HEARTBEAT_TIMEOUT / divisor,
            catch_up=tuple(d / divisor for d in CATCH_UP_DELAYS),
        )


class Rejected(Exception):
    """A document the fallback chain refuses, with the cause fetch_failed reports."""

    def __init__(self, cause: str, message: str) -> None:
        super().__init__(message)
        self.cause = cause


class Host(Protocol):
    """What the thread needs from its client."""

    def held_version(self) -> int: ...

    def accept(self, document: bytes) -> list[str]:
        """Applies a fetched document and returns the flags it may have changed.

        Raises Rejected when the fallback chain refuses the document.
        """

    def fetch_succeeded(self, changed: list[str]) -> None: ...

    def fetch_failed(self, cause: str, status: int, error: str) -> None: ...

    def key_revoked(self, status: int) -> None: ...

    def log(self, level: int, event: str, **fields: Any) -> None: ...

    def mark_ready(self) -> None: ...


# Doublings beyond this reach any limit from any base; capping the exponent keeps a long outage's
# count of failures from overflowing the float the ceiling is computed in.
_MAX_DOUBLINGS = 64


def backoff_ceiling(failures: int, base: float, limit: float) -> float:
    """The exponential ceiling of the nth consecutive failure: the base doubling up to the limit."""
    doublings = min(max(failures - 1, 0), _MAX_DOUBLINGS)
    return min(base * 2.0**doublings, limit)


def full_jitter(ceiling: float) -> float:
    """Draws a wait uniformly from [0, ceiling]."""
    wait: float = random.uniform(0.0, ceiling)
    return wait


def user_agent(version: str, platform: str) -> str:
    return f"flagnetic-python/{version} ({platform})"


class _Unbuffered(http.client.HTTPResponse):
    """A response that reads no further than the end of the headers.

    The ruleset stream's body is read from the socket by the delivery thread, so
    nothing of it may sit in a buffer of ``http.client`` where ``select`` cannot see it.
    """

    def __init__(self, sock: socket.socket, *args: Any, **kwargs: Any) -> None:
        super().__init__(sock, *args, **kwargs)
        # getresponse closes the connection of a response that will close, which leaves
        # the socket to the response; keep a handle to it.
        self.sock = sock
        self.fp.close()
        self.fp = sock.makefile("rb", buffering=0)  # type: ignore[assignment]


class _StreamConnection(http.client.HTTPConnection):
    response_class = _Unbuffered


class _SecureStreamConnection(http.client.HTTPSConnection):
    response_class = _Unbuffered


class Delivery:
    """Keeps a client's ruleset fresh from the server until closed."""

    def __init__(
        self,
        host: Host,
        *,
        base_url: str,
        sdk_key: str,
        waits: Waits,
        stream: bool,
        agent: str,
        schema_version: int,
        ssl_context: ssl.SSLContext | None = None,
        sleep_hook: Callable[[], None] | None = None,
    ) -> None:
        self._host = host
        parts = urlsplit(base_url)
        self._secure = parts.scheme == "https"
        self._netloc_host = parts.hostname or ""
        self._port = parts.port
        prefix = parts.path.rstrip("/")
        self._ruleset_path = f"{prefix}{API_PATH}/ruleset"
        self._stream_path = f"{prefix}{API_PATH}/stream"
        self._sdk_key = sdk_key
        self._waits = waits
        self._stream_enabled = stream
        self._agent = agent
        self._schema_version = schema_version
        self._ssl_context = ssl_context
        self._closing = threading.Event()
        self._wake_read, self._wake_write = socket.socketpair()
        self._wake_read.setblocking(False)
        self._thread = threading.Thread(target=self._run, name="flagnetic", daemon=True)

        # Fetching.
        self._next_fetch: float | None = None
        self._fetch_failures = 0
        self._announced = 0
        self._catch_ups = 0
        self._wake_fetch = False
        # The version the ruleset stream last asked for.
        self._target = 0

        # The ruleset stream.
        self._fetch_conn: http.client.HTTPConnection | None = None
        # The stream connection between its request and its response headers.
        self._opening_conn: http.client.HTTPConnection | None = None
        self._conn: http.client.HTTPConnection | None = None
        self._resp: http.client.HTTPResponse | None = None
        self._stream_sock: socket.socket | None = None
        self._chunks: ChunkedDecoder | None = None
        self._parser = EventStreamParser()
        self._stream_connected = False
        self._stream_stopped = False
        self._stream_failures = 0
        self._next_stream = 0.0
        self._stream_started = 0.0
        self._last_data = 0.0
        self._first_attempt_done = not stream
        self._first_connect = True

    def start(self) -> None:
        self._thread.start()

    def stop(self, timeout: float | None) -> None:
        """Stops the thread and waits for it for at most ``timeout`` seconds."""
        self._closing.set()
        with contextlib.suppress(OSError):
            self._wake_write.send(b"x")
        # A request in flight ends now rather than at its timeout.
        for sock in (
            getattr(self._fetch_conn, "sock", None),
            getattr(self._opening_conn, "sock", None),
            self._stream_sock,
        ):
            if sock is not None:
                with contextlib.suppress(OSError):
                    sock.shutdown(socket.SHUT_RDWR)
        if self._thread.is_alive() and threading.current_thread() is not self._thread:
            self._thread.join(timeout)

    # -- the loop ---------------------------------------------------------

    def _run(self) -> None:
        try:
            self._loop()
        except Exception as err:  # the thread must never die silently with a client waiting on it
            self._host.log(
                logging.ERROR, _events.EVALUATION_FAILED, flag="", cause=f"delivery thread: {err!r}"
            )
        finally:
            self._drop_stream_connection()
            self._host.mark_ready()
            with contextlib.suppress(OSError):
                self._wake_read.close()
                self._wake_write.close()

    def _loop(self) -> None:
        while not self._closing.is_set():
            now = time.monotonic()
            if self._should_open_stream(now):
                self._open_stream()
                continue
            if self._next_fetch is None and self._first_attempt_done:
                self._next_fetch = now
            if self._next_fetch is not None and (now >= self._next_fetch or self._wake_fetch):
                self._wake_fetch = False
                self._fetch_once()
                continue
            self._wait(now)

    def _should_open_stream(self, now: float) -> bool:
        return (
            self._stream_enabled
            and not self._stream_stopped
            and self._conn is None
            and now >= self._next_stream
        )

    def _wait(self, now: float) -> None:
        """Waits for the next reason to act: bytes on the stream, a due time, or a close."""
        deadlines: list[float] = []
        if self._next_fetch is not None:
            deadlines.append(self._next_fetch)
        if self._conn is not None:
            deadlines.append(self._last_data + self._waits.heartbeat)
        elif self._stream_enabled and not self._stream_stopped:
            deadlines.append(self._next_stream)
        timeout = max(min(deadlines) - now, 0.0) if deadlines else None
        readable: list[socket.socket] = [self._wake_read]
        sock = self._stream_sock
        if sock is not None:
            pending = getattr(sock, "pending", None)
            if pending is not None and pending() > 0:
                timeout = 0.0
            readable.append(sock)
        ready, _, _ = select.select(readable, [], [], timeout)
        if self._wake_read in ready:
            with contextlib.suppress(OSError):
                self._wake_read.recv(4096)
            return
        if sock is not None:
            pending = getattr(sock, "pending", None)
            if sock in ready or (pending is not None and pending() > 0):
                self._pump()
            elif time.monotonic() >= self._last_data + self._waits.heartbeat:
                self._stream_dropped(CAUSE_SILENCE, f"no frame for {self._waits.heartbeat:g}s", errored=True)

    # -- fetching ---------------------------------------------------------

    def _connection(self, secure: bool, stream: bool) -> http.client.HTTPConnection:
        timeout = self._waits.request_timeout
        if secure:
            cls = _SecureStreamConnection if stream else http.client.HTTPSConnection
            return cls(self._netloc_host, self._port, timeout=timeout, context=self._ssl_context)
        cls_plain = _StreamConnection if stream else http.client.HTTPConnection
        return cls_plain(self._netloc_host, self._port, timeout=timeout)

    def _fetch_once(self) -> None:
        outcome = self._fetch()
        if outcome == _FETCH_CLOSING or self._closing.is_set():
            return
        self._host.mark_ready()
        now = time.monotonic()
        if outcome == _FETCH_FAILED:
            self._fetch_failures += 1
            wait = full_jitter(
                backoff_ceiling(self._fetch_failures, self._waits.retry_base, self._waits.retry_cap)
            )
        elif outcome == _FETCH_UNAUTHORIZED:
            self._fetch_failures += 1
            wait = full_jitter(
                backoff_ceiling(self._fetch_failures, self._waits.retry_base, self._waits.unauthorized_cap)
            )
        else:
            self._fetch_failures = 0
            if self._target != self._announced:
                self._announced, self._catch_ups = self._target, 0
            if self._host.held_version() < self._announced and self._catch_ups < len(self._waits.catch_up):
                # The replica that answered is still behind the version the event carried:
                # ask again before the safety poll.
                wait = self._waits.catch_up[self._catch_ups]
                self._catch_ups += 1
            else:
                wait = self._waits.poll if self._stream_connected else self._waits.down_poll
        self._next_fetch = now + wait

    def _fetch(self) -> str:
        host = self._host
        headers = {
            "Authorization": f"Bearer {self._sdk_key}",
            "User-Agent": self._agent,
            "Flagnetic-Schema-Version": str(self._schema_version),
            "Accept-Encoding": "gzip",
        }
        held = host.held_version()
        if held:
            headers["If-None-Match"] = f'"{held}"'
        deadline = time.monotonic() + self._waits.request_timeout
        conn = self._connection(self._secure, stream=False)
        self._fetch_conn = conn
        try:
            conn.connect()
            # A close that came while connecting found no socket to shut down.
            if self._closing.is_set():
                return _FETCH_CLOSING
            conn.request("GET", self._ruleset_path, headers=headers)
            resp = conn.getresponse()
            status = resp.status
            if status == 304:
                if held:
                    host.fetch_succeeded([])
                    return _FETCH_OK
                host.fetch_failed(CAUSE_HTTP_STATUS, status, "304 with no ruleset held")
                return _FETCH_FAILED
            if status in (401, 403):
                resp.close()
                host.key_revoked(status)
                host.fetch_failed(CAUSE_UNAUTHORIZED, status, f"{status} {resp.reason}")
                return _FETCH_UNAUTHORIZED
            if status != 200:
                host.fetch_failed(CAUSE_HTTP_STATUS, status, f"{status} {resp.reason}")
                return _FETCH_FAILED
            document = self._read_body(resp, deadline)
        except TimeoutError as err:
            return self._failed(CAUSE_TIMEOUT, err)
        except (OSError, http.client.HTTPException, ValueError, zlib.error, EOFError) as err:
            return self._failed(CAUSE_NETWORK, err)
        finally:
            self._fetch_conn = None
            conn.close()
        if self._closing.is_set():
            return _FETCH_CLOSING
        try:
            changed = host.accept(document)
        except Rejected as err:
            host.fetch_failed(err.cause, 0, str(err))
            return _FETCH_FAILED
        host.fetch_succeeded(changed)
        return _FETCH_OK

    def _failed(self, cause: str, err: BaseException) -> str:
        if self._closing.is_set():
            return _FETCH_CLOSING
        self._host.fetch_failed(cause, 0, str(err) or type(err).__name__)
        return _FETCH_FAILED

    @staticmethod
    def _read_body(resp: http.client.HTTPResponse, deadline: float) -> bytes:
        """The body, gunzipped when the server did, read in pieces so that a slow one meets the deadline."""
        gzipped = (resp.getheader("Content-Encoding") or "").lower() == "gzip"
        parts: list[bytes] = []
        size = 0
        while True:
            if time.monotonic() > deadline:
                raise TimeoutError("ruleset request deadline exceeded")
            piece = resp.read(1 << 16)
            if not piece:
                break
            parts.append(piece)
            size += len(piece)
            if not gzipped and size > MAX_RULESET_BYTES:
                raise ValueError(f"ruleset larger than {MAX_RULESET_BYTES} bytes")
        body = b"".join(parts)
        if not gzipped:
            return body
        decompressor = zlib.decompressobj(16 + zlib.MAX_WBITS)
        out = decompressor.decompress(body, MAX_RULESET_BYTES + 1)
        if len(out) > MAX_RULESET_BYTES:
            raise ValueError(f"ruleset larger than {MAX_RULESET_BYTES} bytes")
        if not decompressor.eof:
            raise EOFError("gzip stream ends early")
        return out

    # -- the ruleset stream ------------------------------------------------

    def _open_stream(self) -> None:
        host = self._host
        headers = {
            "Authorization": f"Bearer {self._sdk_key}",
            "User-Agent": self._agent,
            "Accept": "text/event-stream",
            "Accept-Encoding": "identity",
            "Cache-Control": "no-cache",
        }
        # The held version is where the stream resumes: the server sends the current
        # version at once when it is above, nothing when it is not.
        last_event_id = host.held_version()
        if last_event_id > 0:
            headers["Last-Event-ID"] = str(last_event_id)
        conn = self._connection(self._secure, stream=True)
        # Held where close can shut it down, so a server slow to answer never delays a close.
        self._opening_conn = conn
        try:
            conn.connect()
            # A close that came while connecting found no socket to shut down.
            if self._closing.is_set():
                conn.close()
                return
            conn.request("GET", self._stream_path, headers=headers)
            resp = conn.getresponse()
        except TimeoutError as err:
            conn.close()
            self._first_attempt_done = True
            if not self._closing.is_set():
                self._stream_dropped_before_open(CAUSE_TIMEOUT, err, 0)
            return
        except (OSError, http.client.HTTPException) as err:
            conn.close()
            self._first_attempt_done = True
            if not self._closing.is_set():
                self._stream_dropped_before_open(CAUSE_NETWORK, err, 0)
            return
        finally:
            self._opening_conn = None
        # The first fetch waits for this attempt, so that the stream is open before it
        # and no change falls between the two.
        self._first_attempt_done = True
        if resp.status in (401, 403):
            conn.close()
            host.key_revoked(resp.status)
            host.log(
                logging.WARNING,
                _events.SSE_DROPPED,
                cause=CAUSE_UNAUTHORIZED,
                error=f"stream: {resp.status} {resp.reason}",
                reconnect=False,
                status=resp.status,
            )
            self._stream_stopped = True
            return
        if resp.status != 200:
            conn.close()
            self._stream_dropped_before_open(
                CAUSE_HTTP_STATUS, f"stream: {resp.status} {resp.reason}", resp.status
            )
            return
        sock: socket.socket = cast(_Unbuffered, resp).sock
        sock.settimeout(self._waits.request_timeout)
        self._conn = conn
        self._resp = resp
        self._stream_sock = sock
        self._chunks = ChunkedDecoder() if resp.chunked else None
        self._parser = EventStreamParser()
        self._stream_connected = True
        now = time.monotonic()
        self._stream_started = self._last_data = now
        host.log(logging.INFO, _events.SSE_CONNECTED, last_event_id=last_event_id)
        if not self._first_connect:
            # Versions may have been missed while the stream was down.
            self._wake_fetch = True
        self._first_connect = False

    def _stream_dropped_before_open(self, cause: str, err: object, status: int) -> None:
        fields: dict[str, Any] = {"cause": cause, "error": str(err), "reconnect": True}
        if status:
            fields["status"] = status
        self._host.log(logging.WARNING, _events.SSE_DROPPED, **fields)
        self._stream_failures += 1
        self._next_stream = time.monotonic() + full_jitter(
            backoff_ceiling(self._stream_failures, self._waits.retry_base, self._waits.stream_retry_cap)
        )

    def _pump(self) -> None:
        """Reads what the stream has sent and acts on every ruleset event in it."""
        sock = self._stream_sock
        assert sock is not None
        try:
            data = sock.recv(1 << 16)
        except TimeoutError as err:
            self._stream_dropped(CAUSE_TIMEOUT, str(err), errored=True)
            return
        except (OSError, ssl.SSLError) as err:
            if self._closing.is_set():
                return
            self._stream_dropped(CAUSE_NETWORK, str(err) or type(err).__name__, errored=True)
            return
        if not data:
            self._stream_dropped(CAUSE_SERVER_CLOSED, "EOF", errored=False)
            return
        try:
            body = self._chunks.feed(data) if self._chunks is not None else data
            frames = self._parser.feed(body) if body else []
        except StreamError as err:
            self._stream_dropped(CAUSE_MALFORMED, str(err), errored=True)
            return
        for frame in frames:
            self._last_data = time.monotonic()
            if not self._frame(frame):
                return
        if self._chunks is not None and self._chunks.finished:
            self._stream_dropped(CAUSE_SERVER_CLOSED, "final chunk", errored=False)

    def _frame(self, frame: Frame) -> bool:
        """Acts on a frame; False when the stream was dropped over it."""
        if frame.comment or frame.event != RULESET_EVENT:
            return True
        try:
            announced = json.loads(frame.data)
            version = announced["version"]
            if type(version) is not int:
                raise TypeError("version is not an integer")
        except (ValueError, KeyError, TypeError) as err:
            # A ruleset event this SDK cannot read means the stream is not what it claims
            # to be: drop it, and the fetch that follows the reconnect finds whatever was announced.
            self._stream_dropped(CAUSE_MALFORMED, str(err), errored=True)
            return False
        if version > self._host.held_version():
            self._target = max(self._target, version)
            self._wake_fetch = True
        return True

    def _stream_dropped(self, cause: str, error: str, *, errored: bool) -> None:
        """Logs sse_dropped, closes the connection and schedules the reconnect."""
        level = logging.WARNING if errored or cause != CAUSE_SERVER_CLOSED else logging.INFO
        self._host.log(level, _events.SSE_DROPPED, cause=cause, error=error, reconnect=True)
        now = time.monotonic()
        held_for = now - self._stream_started
        self._drop_stream_connection()
        if held_for >= self._waits.stable_for:
            self._stream_failures = 0
        if errored:
            self._stream_failures += 1
            wait = full_jitter(
                backoff_ceiling(self._stream_failures, self._waits.retry_base, self._waits.stream_retry_cap)
            )
        else:
            # The server's own retry hint, never longer than the protocol's window, so a
            # deploy's streams come back spread out.
            ceiling = self._waits.close_jitter_cap
            retry = self._parser.retry
            if retry is not None and 0 < retry < ceiling:
                ceiling = retry
            wait = full_jitter(ceiling)
        self._next_stream = now + wait

    def _drop_stream_connection(self) -> None:
        conn, self._conn = self._conn, None
        resp, self._resp = self._resp, None
        self._stream_sock = None
        self._chunks = None
        self._stream_connected = False
        if resp is not None:
            resp.close()
        if conn is not None:
            conn.close()
