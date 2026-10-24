"""Local overrides: flag values substituted inside this process, never sent to the server."""

from __future__ import annotations

import builtins
import copy
import logging
import threading
from collections.abc import Callable
from typing import TYPE_CHECKING, Any, NamedTuple

from . import _events

if TYPE_CHECKING:
    from ._eval import Ruleset


class LocalOverridesNotAllowed(Exception):
    """Raised by ``local_overrides.set`` on a client built without ``allow_overrides=True``."""

    def __init__(self) -> None:
        super().__init__("flagnetic: local overrides need allow_overrides=True")


class LocalOverrideRefused(Exception):
    """Raised by ``local_overrides.set`` while the held ruleset belongs to a protected environment."""

    def __init__(self, key: str) -> None:
        super().__init__(f"flagnetic: the local override of {key!r} is refused in a protected environment")


class LocalOverride(NamedTuple):
    type: str
    value: Any


def type_of(value: Any) -> str:
    """The flag type a value belongs to: boolean, string, number, anything else JSON."""
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, str):
        return "string"
    if isinstance(value, (int, float)):
        return "number"
    return "json"


def fits(flag_type: str, value: Any) -> bool:
    if flag_type == "json":
        return True
    return type_of(value) == flag_type


class LocalOverrides:
    """Local overrides of one client.

    An overridden flag serves its override with reason ``local_override``, no
    variation and no experiment block, and emits no exposure; it is counted in
    the evaluation summary like any served value. They exist only with
    ``allow_overrides=True`` and are refused, and dropped, in a protected
    environment. Change listeners hear every change.
    """

    def __init__(
        self,
        allowed: bool,
        held: Callable[[], Ruleset | None],
        log: Callable[..., None],
        changed: Callable[[list[str]], None],
    ) -> None:
        self._allowed = allowed
        self._held = held
        self._log = log
        self._changed = changed
        self._lock = threading.Lock()
        # Replaced, never changed in place, so a getter reads it without the lock.
        self.by_key: dict[str, LocalOverride] = {}

    def set(self, key: str, value: Any) -> None:
        """Serves ``value`` for the flag ``key`` in place of what the ruleset serves.

        Raises ``LocalOverridesNotAllowed`` without ``allow_overrides=True`` and
        ``LocalOverrideRefused`` while the held ruleset's environment is
        protected, logging ``override_refused`` either way. The value must be of
        the flag's type, or for a key the ruleset lacks it gives the type;
        another type raises ``TypeError``.
        """
        if not self._allowed:
            self._log(logging.WARNING, _events.OVERRIDE_REFUSED, flag=key, cause="not_allowed")
            raise LocalOverridesNotAllowed
        ruleset = self._held()
        if ruleset is not None and ruleset.protected:
            self._log(
                logging.WARNING,
                _events.OVERRIDE_REFUSED,
                flag=key,
                cause="protected_environment",
                environment=ruleset.environment_key,
            )
            raise LocalOverrideRefused(key)
        flag = ruleset.flag(key) if ruleset is not None else None
        flag_type = flag.type if flag is not None else type_of(value)
        if not fits(flag_type, value):
            raise TypeError(f"flagnetic: a local override of {key!r} must be a {flag_type} value")
        if flag_type == "json":
            value = copy.deepcopy(value)
        with self._lock:
            self.by_key = {**self.by_key, key: LocalOverride(flag_type, value)}
        self._log(logging.WARNING, _events.OVERRIDE_SET, flag=key)
        self._changed([key])

    def remove(self, key: str) -> None:
        """Drops the local override of ``key``; change listeners hear it when there was one."""
        with self._lock:
            if key not in self.by_key:
                return
            self.by_key = {k: o for k, o in self.by_key.items() if k != key}
        self._changed([key])

    def clear(self) -> None:
        """Drops every local override; change listeners hear their keys."""
        with self._lock:
            keys = sorted(self.by_key)
            self.by_key = {}
        if keys:
            self._changed(keys)

    def after_fork(self) -> None:
        """A forked child makes its lock anew: a thread of the parent may have held it at the fork."""
        self._lock = threading.Lock()

    def list(self) -> dict[str, Any]:
        """The local overrides by flag key."""
        return {key: copy.deepcopy(o.value) for key, o in self.by_key.items()}

    def refuse(self, ruleset: Ruleset) -> builtins.list[str]:
        """Drops every local override when ``ruleset`` is of a protected environment; returns their keys."""
        if not ruleset.protected:
            return []
        with self._lock:
            keys = sorted(self.by_key)
            self.by_key = {}
        for key in keys:
            self._log(
                logging.WARNING,
                _events.OVERRIDE_REFUSED,
                flag=key,
                cause="protected_environment",
                environment=ruleset.environment_key,
            )
        return keys
