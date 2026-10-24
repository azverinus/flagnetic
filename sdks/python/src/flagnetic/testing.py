"""Offline clients for deterministic tests, from a map of values, a ruleset document or a file.

No client built here touches the network or starts a thread::

    from flagnetic import testing

    client = testing.from_values({"new_checkout": True})
    handler = make_handler(client)
    client.set("new_checkout", False)  # the flag now serves False to everyone

What the client would have reported, evaluation counts, exposures and
aliases, is in ``client.recorder``.
"""

from __future__ import annotations

import json
import os
import threading
from collections.abc import Callable, Mapping
from pathlib import Path
from typing import Any

from ._client import Client, Document, _document_bytes
from ._overrides import type_of
from ._reporting import Recorder

__all__ = ["TestClient", "from_document", "from_file", "from_values"]

# The one variation set and from_values give a flag.
_VARIATION = "value"


class TestClient(Client):
    """An offline client whose ruleset document a test can rewrite.

    Build one with ``from_values``, ``from_document`` or ``from_file``; every
    option of ``Client`` but ``offline`` applies.
    """

    # Not a test class, whatever its name says to pytest.
    __test__ = False

    def __init__(self, read: Callable[[], Document], **options: Any) -> None:
        self._read = read
        self._document_lock = threading.Lock()
        self._document: dict[str, Any] = _parse(read())
        super().__init__("", "", offline=self._current, **options)

    @property
    def recorder(self) -> Recorder:
        """The evaluation counts, exposures and aliases the client would have reported."""
        recorder = super().recorder
        assert recorder is not None
        return recorder

    def set(self, key: str, value: Any) -> None:
        """Makes the flag ``key`` serve ``value`` to everyone, firing the change listeners.

        The flag gets one variation, served as fallthrough, so the reason reads
        ``fallthrough``. A key the document lacks is added, typed by the value
        as ``from_values`` types it; an existing flag keeps its type, and a
        value of another type raises ``TypeError``.
        """
        with self._document_lock:
            document = json.loads(json.dumps(self._document))
            _set_flag(document, key, value)
            self._document = document
        self._reload()

    def reload(self) -> None:
        """Reads the source the client was built from again, discarding what ``set`` changed.

        The change listeners hear the flags that changed. A source that no
        longer decodes raises ``ValueError`` and the held ruleset keeps serving.
        """
        document = _parse(self._read())
        with self._document_lock:
            self._document = document
        self._reload()

    def _current(self) -> Document:
        with self._document_lock:
            return self._document


def from_values(values: Mapping[str, Any], **options: Any) -> TestClient:
    """An offline client serving each value as the fallthrough of a flag with one variation.

    The value's type gives the flag type: ``bool`` is boolean, ``str`` is
    string, ``int`` and ``float`` are number, anything else is JSON.
    """
    document: dict[str, Any] = {"schema_version": 1, "version": 1, "scope": "server", "flags": []}
    for key, value in values.items():
        _set_flag(document, key, value)
    text = json.dumps(document)
    return TestClient(lambda: text, **options)


def from_document(document: Document, **options: Any) -> TestClient:
    """An offline client from a ruleset document, exactly as the ruleset endpoint serves it.

    Raises ``ValueError`` when the document does not decode.
    """
    text = _document_bytes(document)
    return TestClient(lambda: text, **options)


def from_file(path: str | os.PathLike[str], **options: Any) -> TestClient:
    """An offline client from the ruleset document in the file at ``path``; ``reload`` reads it again."""
    file = Path(path)
    return TestClient(file.read_bytes, **options)


def _parse(document: Document) -> dict[str, Any]:
    try:
        parsed = json.loads(_document_bytes(document))
    except ValueError as err:
        raise ValueError(f"flagnetic: the document is not JSON: {err}") from err
    if not isinstance(parsed, dict):
        raise ValueError("flagnetic: the document is not a JSON object")
    return parsed


def _set_flag(document: dict[str, Any], key: str, value: Any) -> None:
    """Replaces or adds the flag ``key`` in a parsed ruleset document."""
    flags = document.get("flags")
    if not isinstance(flags, list):
        flags = []
    flag_type = type_of(value)
    at = next((i for i, f in enumerate(flags) if isinstance(f, dict) and f.get("key") == key), None)
    if at is not None:
        existing = flags[at].get("type")
        if isinstance(existing, str):
            if existing not in ("json", flag_type):
                raise TypeError(f"flagnetic: flag {key!r} is {existing}, the value is {flag_type}")
            flag_type = existing
    flag = {
        "key": key,
        "type": flag_type,
        "automatic_exposure": False,
        "on": True,
        "variations": [{"key": _VARIATION, "value": value}],
        "off_variation": _VARIATION,
        "fallthrough": _VARIATION,
    }
    if at is None:
        flags.append(flag)
    else:
        flags[at] = flag
    document["flags"] = flags
