"""A response marked deprecated is logged once per operation."""

from __future__ import annotations

import contextlib
import http.client
import logging
import threading
from collections.abc import Callable
from typing import Any

from . import events

# The SDK API lives under this path; a base URL's own path prefix is not part of an operation.
_API = "/sdk/v1"


class DeprecationNotes:
    """Logs the first ``Deprecation`` header seen for each operation, and nothing after it.

    The server marks an operation deprecated (RFC 9745) and may add its removal date in ``Sunset``
    (RFC 8594). The warning is for the operator, so it never raises and never changes the answer
    the call acts on.
    """

    def __init__(self, log: Callable[..., None]) -> None:
        self._log = log
        self._lock = threading.Lock()
        self._seen: set[str] = set()

    def observe(self, method: str, path: str, resp: http.client.HTTPResponse) -> None:
        with contextlib.suppress(Exception):
            self._observe(method, path, resp)

    def _observe(self, method: str, path: str, resp: http.client.HTTPResponse) -> None:
        deprecation = resp.getheader("Deprecation")
        if not deprecation:
            return
        _, _, tail = path.partition(_API)
        operation = f"{method} {_API}{tail}".split("?", 1)[0]
        with self._lock:
            if operation in self._seen:
                return
            self._seen.add(operation)
        fields: dict[str, Any] = {"operation": operation, "deprecation": deprecation}
        sunset = resp.getheader("Sunset")
        if sunset:
            fields["sunset"] = sunset
        self._log(logging.WARNING, events.DEPRECATED_OPERATION, **fields)
