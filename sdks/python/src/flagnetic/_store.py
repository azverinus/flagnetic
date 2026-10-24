"""The file that keeps the last good ruleset across restarts."""

from __future__ import annotations

import contextlib
import os
import tempfile
from pathlib import Path


def read(path: Path) -> tuple[bytes, float] | None:
    """The document at ``path`` and when it was written, or None when there is no file (a first start)."""
    try:
        return path.read_bytes(), path.stat().st_mtime
    except FileNotFoundError:
        return None


def write(path: Path, document: bytes) -> None:
    """Replaces the file by rename, so that a crash never leaves half a document.

    The file is readable by its owner only, since a ruleset carries private segments.
    """
    # mkstemp creates the file with mode 0600.
    fd, tmp = tempfile.mkstemp(prefix=f"{path.name}.", suffix=".tmp", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as out:
            out.write(document)
            out.flush()
            os.fsync(out.fileno())
        os.replace(tmp, path)
    except BaseException:
        with contextlib.suppress(OSError):
            os.unlink(tmp)
        raise
