"""An HTTP server that plays the Flagnetic SDK API as a script says, over real sockets."""

from __future__ import annotations

import queue
import threading
import time
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any


@dataclass
class Request:
    method: str
    path: str
    headers: Mapping[str, str]
    arrived: float = field(default_factory=time.monotonic)


@dataclass
class Response:
    status: int = 200
    headers: Mapping[str, str] = field(default_factory=dict)
    body: bytes = b""
    #: Drop the connection without answering.
    network_error: bool = False
    #: Keep the response open as the change stream, which ``stream_send`` and ``stream_close`` drive.
    stream: bool = False


Handler = Callable[[Request], Response]

_CLOSE = object()


class MockServer:
    """Serves every request through ``handler``; the change stream is driven from the test."""

    def __init__(self, handler: Handler) -> None:
        self._handler = handler
        self._streams: queue.Queue[Any] | None = None
        self._stopping = threading.Event()
        outer = self

        class _Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def do_GET(self) -> None:
                outer._serve(self)

            def log_message(self, format: str, *args: Any) -> None:
                pass

        self._server = ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
        self._server.daemon_threads = True
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)
        self._thread.start()

    @property
    def url(self) -> str:
        host, port = self._server.server_address[:2]
        return f"http://{host!s}:{port}"

    def stream_send(self, data: bytes) -> None:
        """Writes ``data`` to the open change stream."""
        assert self._streams is not None, "no change stream is open"
        self._streams.put(data)

    def stream_close(self) -> None:
        """Ends the open change stream from the server's side."""
        assert self._streams is not None, "no change stream is open"
        self._streams.put(_CLOSE)

    def close(self) -> None:
        self._stopping.set()
        self._server.shutdown()
        self._server.server_close()

    def _serve(self, h: BaseHTTPRequestHandler) -> None:
        request = Request(h.command, h.path, {k.lower(): v for k, v in h.headers.items()})
        response = self._handler(request)
        if response.network_error:
            h.close_connection = True
            return
        h.send_response(response.status)
        for name, value in response.headers.items():
            h.send_header(name, value)
        if response.stream:
            h.send_header("Transfer-Encoding", "chunked")
            h.end_headers()
            h.wfile.flush()
            self._hold_stream(h)
            return
        h.send_header("Content-Length", str(len(response.body)))
        # Every answer ends its connection, so a dropped one is never retried into the next step.
        h.send_header("Connection", "close")
        h.end_headers()
        h.wfile.write(response.body)
        h.close_connection = True

    def _hold_stream(self, h: BaseHTTPRequestHandler) -> None:
        items: queue.Queue[Any] = queue.Queue()
        self._streams = items
        h.close_connection = True
        try:
            while not self._stopping.is_set():
                try:
                    item = items.get(timeout=0.05)
                except queue.Empty:
                    continue
                if item is _CLOSE:
                    h.wfile.write(b"0\r\n\r\n")
                    h.wfile.flush()
                    return
                h.wfile.write(f"{len(item):x}\r\n".encode() + item + b"\r\n")
                h.wfile.flush()
        except OSError:
            return
