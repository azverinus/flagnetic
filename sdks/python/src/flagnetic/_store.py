"""The file that keeps the last good ruleset across restarts.

A store file is a header line naming the key the ruleset was received under, then the ruleset
document exactly as the endpoint served it. The header holds a digest of the key, never the key.
Without it, a process restarted with another environment's key on the same path would serve that
environment's ruleset as last_good, and refuse the right one as a downgrade whenever the old
version was higher.
"""

from __future__ import annotations

import contextlib
import hashlib
import json
import os
import tempfile
from pathlib import Path

_FORMAT = 1


class StoreRefused(Exception):
    """The file is not one to serve from: it belongs to no key, to another key or is in another format."""


def _digest(sdk_key: str) -> str:
    return hashlib.sha256(sdk_key.encode("utf-8")).hexdigest()


def encode(sdk_key: str, document: bytes) -> bytes:
    """The content of a store file holding ``document``, received under ``sdk_key``."""
    header = json.dumps({"flagnetic_store": _FORMAT, "key": _digest(sdk_key)}, separators=(",", ":"))
    return header.encode("ascii") + b"\n" + document


def decode(sdk_key: str, content: bytes) -> bytes:
    """The document in a store file for ``sdk_key``; raises StoreRefused for any other file."""
    line, newline, document = content.partition(b"\n")
    try:
        header = json.loads(line) if newline else None
    except ValueError:
        header = None
    if not isinstance(header, dict) or "flagnetic_store" not in header:
        raise StoreRefused("the store was written under no key")
    fmt = header["flagnetic_store"]
    if type(fmt) is not int or fmt != _FORMAT:
        raise StoreRefused("the store was written in a format this client does not read")
    if header.get("key") != _digest(sdk_key):
        raise StoreRefused("the store was written under another key")
    return document


def read(path: Path, sdk_key: str) -> tuple[bytes, float] | None:
    """The document at ``path`` under ``sdk_key`` and when it was written; None on a first start."""
    try:
        content, written = path.read_bytes(), path.stat().st_mtime
    except FileNotFoundError:
        return None
    return decode(sdk_key, content), written


def write(path: Path, sdk_key: str, document: bytes) -> None:
    """Replaces the file by rename, so that a crash never leaves half a document.

    The file is readable by its owner only, since a ruleset carries private segments.
    """
    # mkstemp creates the file with mode 0600.
    fd, tmp = tempfile.mkstemp(prefix=f"{path.name}.", suffix=".tmp", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as out:
            out.write(encode(sdk_key, document))
            out.flush()
            os.fsync(out.fileno())
        os.replace(tmp, path)
    except BaseException:
        with contextlib.suppress(OSError):
            os.unlink(tmp)
        raise
