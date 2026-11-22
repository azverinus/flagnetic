"""A response marked deprecated is logged once per operation."""

from __future__ import annotations

import contextlib
import http.client
import logging
import threading
from collections.abc import Callable
from typing import Any

from . import events


class DeprecationNotes:
    """Logs the first ``Deprecation`` header seen for each operation, and nothing after it.

    An operation is ``ruleset``, ``stream`` or ``events``.

    The server marks an operation deprecated (RFC 9745) and may add its removal date in ``Sunset``
    (RFC 8594). The warning is for the operator, so it never raises and never changes the answer
    the call acts on.
    """

    def __init__(self, log: Callable[..., None]) -> None:
        self._log = log
        self._lock = threading.Lock()
        self._seen: set[str] = set()

    def observe(self, operation: str, resp: http.client.HTTPResponse) -> None:
        with contextlib.suppress(Exception):
            self._observe(operation, resp)

    def _observe(self, operation: str, resp: http.client.HTTPResponse) -> None:
        deprecation = resp.getheader("Deprecation")
        if not deprecation:
            return
        with self._lock:
            if operation in self._seen:
                return
            self._seen.add(operation)
        fields: dict[str, Any] = {"operation": operation, "deprecation": deprecation}
        sunset = resp.getheader("Sunset")
        if sunset:
            fields["sunset"] = sunset
        self._log(logging.WARNING, events.OPERATION_DEPRECATED, **fields)
