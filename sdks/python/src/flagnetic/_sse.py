"""The change stream's wire format: chunked transfer coding and the event stream."""

from __future__ import annotations

import re
from dataclasses import dataclass

# One line of the change stream. The only event the stream carries is a
# version, so a longer line is a broken or hostile server and ends the
# connection.
MAX_LINE_BYTES = 64 << 10

_LINE_END = re.compile(rb"\r\n|\r|\n")


class StreamError(Exception):
    """The bytes of the change stream are not what the stream claims to be."""


class ChunkedDecoder:
    """Decodes a body in the chunked transfer coding as its bytes arrive.

    ``http.client`` decodes chunks only by blocking on the socket, which a
    thread that also fetches and polls cannot afford; the stream's body is
    read from the socket by the delivery thread, so the coding is undone here.
    """

    def __init__(self) -> None:
        self._buffer = bytearray()
        self._left = 0
        self._state = "size"
        self.finished = False

    def feed(self, data: bytes) -> bytes:
        """Returns the body bytes that ``data`` completes; raises StreamError on a broken coding."""
        self._buffer += data
        out = bytearray()
        while not self.finished:
            if self._state == "size":
                end = self._buffer.find(b"\r\n")
                if end < 0:
                    if len(self._buffer) > MAX_LINE_BYTES:
                        raise StreamError("chunk size line too long")
                    break
                line = bytes(self._buffer[:end]).split(b";", 1)[0].strip()
                del self._buffer[: end + 2]
                try:
                    self._left = int(line, 16)
                except ValueError as err:
                    raise StreamError(f"bad chunk size {line!r}") from err
                if self._left == 0:
                    self.finished = True
                    break
                self._state = "data"
            elif self._state == "data":
                if not self._buffer:
                    break
                take = bytes(self._buffer[: self._left])
                del self._buffer[: len(take)]
                out += take
                self._left -= len(take)
                if self._left == 0:
                    self._state = "crlf"
            else:
                if len(self._buffer) < 2:
                    break
                if self._buffer[:2] != b"\r\n":
                    raise StreamError("chunk not followed by CRLF")
                del self._buffer[:2]
                self._state = "size"
        return bytes(out)


@dataclass(frozen=True, slots=True)
class Frame:
    """One block of the change stream: a dispatched event, or a comment such as the heartbeat."""

    comment: bool = False
    event: str = ""
    id: str = ""
    data: str = ""


class EventStreamParser:
    """Decodes the event stream format from bytes as they arrive.

    The fields ``event``, ``id``, ``data`` and ``retry``, comments, multi-line
    data and lines ended by LF, CRLF or CR. Written by hand so that the package
    depends on the standard library alone.
    """

    def __init__(self) -> None:
        self._pending = b""
        self._event = ""
        self._data: list[str] = []
        self._id = ""
        # The reconnection time the server last asked for, in seconds; None until it sends one.
        self.retry: float | None = None

    def feed(self, data: bytes) -> list[Frame]:
        """Returns the frames ``data`` completes; raises StreamError on a line beyond MAX_LINE_BYTES."""
        buffer = self._pending + data
        frames: list[Frame] = []
        start = 0
        while True:
            match = _LINE_END.search(buffer, start)
            if match is None:
                break
            if match.group() == b"\r" and match.end() == len(buffer):
                # A CR at the end of what has arrived may yet be half of a CRLF.
                break
            self._line(buffer[start : match.start()].decode("utf-8", errors="replace"), frames)
            start = match.end()
        self._pending = buffer[start:]
        if len(self._pending) > MAX_LINE_BYTES:
            raise StreamError("event stream line too long")
        return frames

    def _line(self, line: str, frames: list[Frame]) -> None:
        if line == "":
            # A blank line dispatches, unless the block held no data: an id or
            # a retry on its own only updates the parser.
            if self._data:
                frames.append(Frame(event=self._event, id=self._id, data="\n".join(self._data)))
            self._event = ""
            self._data = []
        elif line.startswith(":"):
            # A comment never interrupts the block being accumulated, but it
            # proves the server is there.
            frames.append(Frame(comment=True))
        else:
            field, _, value = line.partition(":")
            value = value.removeprefix(" ")
            if field == "event":
                self._event = value
            elif field == "data":
                self._data.append(value)
            elif field == "id":
                if "\0" not in value:
                    self._id = value
            elif field == "retry" and value.isascii() and value.isdigit():
                self.retry = int(value) / 1000
