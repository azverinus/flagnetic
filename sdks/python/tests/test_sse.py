"""The ruleset stream's wire format against bytes a broken or hostile server could send."""

from __future__ import annotations

import pytest

from flagnetic._sse import ChunkedDecoder, EventStreamParser, StreamError


@pytest.mark.parametrize(
    "size",
    [b"-5", b"+5", b"0x5", b"1_0", b"", b"5 5", b"f" * 17, b"\xff"],
)
def test_a_chunk_size_that_is_not_plain_hex_digits_is_a_stream_error(size: bytes) -> None:
    with pytest.raises(StreamError):
        ChunkedDecoder().feed(size + b"\r\nhello\r\n")


def test_chunks_are_decoded_across_feeds() -> None:
    decoder = ChunkedDecoder()
    assert decoder.feed(b"5;ext=1\r\nhel") == b"hel"
    assert decoder.feed(b"lo\r\nA\r\n0123456789\r\n") == b"lo0123456789"
    assert not decoder.finished
    assert decoder.feed(b"0\r\n") == b""
    assert decoder.finished


@pytest.mark.parametrize("value", ["9" * 13, "9" * 400, "9" * 5000])
def test_a_retry_of_unbounded_digits_is_a_stream_error(value: str) -> None:
    with pytest.raises(StreamError):
        EventStreamParser().feed(f"retry: {value}\n\n".encode())


def test_a_retry_in_milliseconds_is_read_in_seconds() -> None:
    parser = EventStreamParser()
    parser.feed(b"retry: 1500\n\n")
    assert parser.retry == 1.5
