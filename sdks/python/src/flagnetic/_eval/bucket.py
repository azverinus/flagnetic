"""Unit bucketing: MurmurHash3 over UTF-8, reduced to 10,000 buckets."""

from __future__ import annotations

from struct import unpack_from

BUCKETS = 10_000

_C1 = 0xCC9E2D51
_C2 = 0x1B873593
_MASK = 0xFFFFFFFF


def _utf8(text: str) -> bytes:
    try:
        return text.encode("utf-8")
    except UnicodeEncodeError:
        # A lone surrogate hashes as U+FFFD, the character Go decodes it to.
        return "".join("�" if "\ud800" <= c <= "\udfff" else c for c in text).encode("utf-8")


def murmur3(data: bytes) -> int:
    """MurmurHash3 x86 32-bit with seed 0, read as an unsigned integer."""
    h = 0
    n = len(data)
    blocks = n >> 2
    if blocks:
        for block in unpack_from(f"<{blocks}I", data):
            k = (block * _C1) & _MASK
            k = ((k << 15) | (k >> 17)) & _MASK
            k = (k * _C2) & _MASK
            h ^= k
            h = ((h << 13) | (h >> 19)) & _MASK
            h = (h * 5 + 0xE6546B64) & _MASK
    tail = n & 3
    if tail:
        k = int.from_bytes(data[n - tail :], "little")
        k = (k * _C1) & _MASK
        k = ((k << 15) | (k >> 17)) & _MASK
        k = (k * _C2) & _MASK
        h ^= k
    h ^= n & _MASK
    h ^= h >> 16
    h = (h * 0x85EBCA6B) & _MASK
    h ^= h >> 13
    h = (h * 0xC2B2AE35) & _MASK
    h ^= h >> 16
    return h


def bucket(salt: str, unit_id: str) -> int:
    """The bucket, 0 to 9999, of a unit for a salt: murmur3("{salt}.{unit_id}") mod 10000."""
    return murmur3(_utf8(f"{salt}.{unit_id}")) % BUCKETS
