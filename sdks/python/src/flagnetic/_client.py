"""The client: getters over an immutable decoded ruleset, kept fresh by the delivery thread."""

from __future__ import annotations

import contextlib
import copy
import logging
import math
import os
import platform as platform_module
import threading
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

from . import _events, _store
from ._changes import changed_flags
from ._delivery import (
    CAUSE_PARSE,
    CAUSE_SCHEMA_UNSUPPORTED,
    CAUSE_VERSION_DOWNGRADE,
    CAUSE_WRONG_SCOPE,
    DEFAULT_REQUEST_TIMEOUT,
    Delivery,
    Rejected,
    Waits,
    user_agent,
)
from ._detail import Detail, Experiment, Stage, Status
from ._eval import SCHEMA_VERSION, Context, Ruleset, RulesetError, decode, evaluate, parse
from ._events import Logger, standard_logger
from ._version import __version__

# A client SDK key cannot fetch the ruleset: the ruleset endpoint refuses it.
_CLIENT_KEY_PREFIX = "fn_cli_"

_BOOLEAN = "boolean"
_STRING = "string"
_NUMBER = "number"
_JSON = "json"
# A number flag read through get_int, which serves only values that are whole and fit in an int64.
_INT64 = "int64"
_KNOWN_TYPES = frozenset({_BOOLEAN, _STRING, _NUMBER, _JSON})

_ERROR_NO_RULESET = "no_ruleset"
_ERROR_TYPE_MISMATCH = "type_mismatch"
_ERROR_GENERAL = "general"
_ERROR_FLAG_NOT_FOUND = "flag_not_found"
_ERROR_INCONSISTENT = "inconsistent_ruleset"

_REASON_FALLBACK = "fallback"
_REASON_ERROR = "error"


@dataclass(frozen=True, slots=True)
class _Loaded:
    """A decoded ruleset, the document it was decoded from and when it was received."""

    ruleset: Ruleset
    document: Mapping[str, Any]
    received_at: datetime


class Client:
    """Evaluates flags against a ruleset it keeps fresh in the background.

    Construct it with a server SDK key and the base URL of a Flagnetic
    instance; it returns at once at stage ``built_in``, or ``last_good`` when
    ``store`` holds a ruleset, and fetches in the background. Call ``ready`` to
    wait for the first fetch. Construction fails only for a client SDK key
    (``ValueError``) or an unusable argument: network trouble is never a
    construction error.

    Every getter is total: it never raises and never does I/O, and serves the
    built-in default with reason ``fallback`` when it cannot serve the flag.
    The context comes first, as every call on a request path carries one. A
    client is safe for concurrent use.

    Options, all keyword-only:

    ``poll_interval``
        Seconds between ruleset fetches (with ``If-None-Match``) while the
        change stream is connected; default 300, at least 30. While the stream
        is down or disabled the safety poll runs every 30 seconds instead, so
        a kill switch is never further away than that.
    ``stream``
        Whether to hold the change stream; on by default. It carries nothing
        but the new ruleset version, so a kill switch reaches the client
        within two seconds; with it off the safety poll is the only notice,
        which is what a proxy that blocks ``text/event-stream`` leaves.
    ``request_timeout``
        Seconds a ruleset request may take; default 10.
    ``store``
        A file path. Every accepted ruleset is written there, readable by its
        owner only, and loaded at construction as ``last_good`` before any
        network call. A ruleset the store cannot load or save logs
        ``store_failed``; a corrupt file leaves the client at ``built_in``.
    ``logger``
        A hook called with ``(level, event, fields)`` for every log event, from
        any thread; the default sends them to the ``flagnetic`` logger of
        ``logging``.
    """

    def __init__(
        self,
        sdk_key: str,
        base_url: str,
        *,
        poll_interval: float | None = None,
        stream: bool = True,
        request_timeout: float = DEFAULT_REQUEST_TIMEOUT,
        store: str | os.PathLike[str] | None = None,
        logger: Logger | None = None,
        _time_divisor: float = 1.0,
    ) -> None:
        if not isinstance(sdk_key, str) or not sdk_key:
            raise ValueError("flagnetic: the SDK key is empty")
        if sdk_key.startswith(_CLIENT_KEY_PREFIX):
            raise ValueError(
                "flagnetic: a client SDK key cannot fetch the ruleset; the Python SDK needs a server key"
            )
        parts = urlsplit(base_url)
        if parts.scheme not in ("http", "https") or not parts.hostname:
            raise ValueError(f"flagnetic: base_url {base_url!r} is not an http or https URL")
        if request_timeout <= 0:
            raise ValueError("flagnetic: request_timeout must be positive")
        if poll_interval is not None and poll_interval <= 0:
            raise ValueError("flagnetic: poll_interval must be positive")

        self._logger: Logger = logger if logger is not None else standard_logger
        self._loaded: _Loaded | None = None
        self._stage = Stage.BUILT_IN
        self._stale_since: datetime | None = None
        self._revoked = False
        self._schema_logged = False
        self._ready = threading.Event()
        self._closed = False
        self._lock = threading.Lock()
        self._listeners: dict[int, Callable[[list[str]], None]] = {}
        self._stage_listeners: dict[int, Callable[[Stage], None]] = {}
        self._next_listener = 0
        # The flag keys already logged as type_mismatch, and by flag the ruleset
        # version whose unsupported rules were last logged.
        self._mismatched: set[str] = set()
        self._unsupported_logged: dict[str, int] = {}
        self._store_path = Path(store) if store is not None else None
        if self._store_path is not None:
            self._load_store(self._store_path)

        self._delivery = Delivery(
            _Host(self),
            base_url=base_url,
            sdk_key=sdk_key,
            waits=Waits.of(request_timeout, poll_interval, _time_divisor),
            stream=stream,
            agent=user_agent(__version__, platform_module.system().lower() or "unknown"),
            schema_version=SCHEMA_VERSION,
        )
        self._delivery.start()

    # -- getters ----------------------------------------------------------

    def get_bool(self, context: Context, key: str, default: bool) -> bool:
        """Serves a boolean flag for ``context``, or ``default`` with reason ``fallback``."""
        value = self._evaluate(context, key, _BOOLEAN, default).value
        return value if isinstance(value, bool) else default

    def get_string(self, context: Context, key: str, default: str) -> str:
        """Serves a string flag for ``context``, or ``default`` with reason ``fallback``."""
        value = self._evaluate(context, key, _STRING, default).value
        return value if isinstance(value, str) else default

    def get_number(self, context: Context, key: str, default: float) -> float:
        """Serves a number flag for ``context`` as a double, or ``default`` with reason ``fallback``."""
        value = self._evaluate(context, key, _NUMBER, default).value
        return float(value) if isinstance(value, (int, float)) and not isinstance(value, bool) else default

    def get_int(self, context: Context, key: str, default: int) -> int:
        """Serves a number flag for ``context`` as an int, or ``default`` with reason ``fallback``.

        A served number that is not whole or does not fit in 64 bits serves
        ``default`` with error ``type_mismatch`` rather than a truncated value.
        """
        value = self._evaluate(context, key, _INT64, default).value
        return value if type(value) is int else default

    def get_json(self, context: Context, key: str, default: Any) -> Any:
        """Serves a JSON flag for ``context``, or ``default`` with reason ``fallback``.

        The value is a copy: the caller may keep or modify it.
        """
        return self._evaluate(context, key, _JSON, default).value

    def get_detail(self, context: Context, key: str, default: Any) -> Detail:
        """Serves a flag for ``context`` with why it was served.

        The type of ``default`` names the flag type asked for: ``bool`` is
        boolean, ``str`` is string, ``float`` is number, ``int`` is a number
        read as ``get_int`` reads it, anything else is JSON.
        """
        if isinstance(default, bool):
            requested = _BOOLEAN
        elif isinstance(default, str):
            requested = _STRING
        elif isinstance(default, int):
            requested = _INT64
        elif isinstance(default, float):
            requested = _NUMBER
        else:
            requested = _JSON
        return self._evaluate(context, key, requested, default)

    def _evaluate(self, context: Context, key: str, requested: str, default: Any) -> Detail:
        """The API boundary every getter goes through: it never raises."""
        loaded = self._loaded
        try:
            return self._detail(loaded, context, key, requested, default)
        except Exception as err:
            self._log(logging.ERROR, _events.EVALUATION_FAILED, flag=key, cause=repr(err))
            return self._fallback(loaded, default, _ERROR_GENERAL)

    def _detail(
        self, loaded: _Loaded | None, context: Context, key: str, requested: str, default: Any
    ) -> Detail:
        if loaded is None:
            return self._fallback(None, default, _ERROR_NO_RULESET)
        flag_type = _NUMBER if requested == _INT64 else requested
        flag = loaded.ruleset.flag(key)
        if flag is None or flag.type not in _KNOWN_TYPES:
            # A flag of a type this SDK does not know is absent, so a server that adds a
            # flag type never breaks an older SDK's other flags.
            return self._fallback(loaded, default, _ERROR_FLAG_NOT_FOUND)
        if flag.type != flag_type:
            self._mismatch(key, flag.type, requested)
            return self._fallback(loaded, default, _ERROR_TYPE_MISMATCH)
        result = evaluate(loaded.ruleset, context, key)
        if result.reason == _REASON_ERROR:
            return self._fallback(loaded, default, result.error_code)
        served = _served_value(flag_type, result.value)
        if served is _INVALID:
            return self._fallback(loaded, default, _ERROR_INCONSISTENT)
        if requested == _INT64:
            served = _as_int64(served)
            if served is _INVALID:
                self._mismatch(key, _NUMBER, requested)
                return self._fallback(loaded, default, _ERROR_TYPE_MISMATCH)
        experiment = result.experiment
        return Detail(
            value=served,
            reason=result.reason,
            variation=result.variation or "",
            rule_id=result.rule_id,
            ruleset_version=loaded.ruleset.version,
            received_at=loaded.received_at,
            stage=self._stage,
            overridden=result.override_influenced,
            experiment=None if experiment is None else Experiment(**experiment.as_dict()),
        )

    def _fallback(self, loaded: _Loaded | None, default: Any, code: str) -> Detail:
        return Detail(
            value=default,
            reason=_REASON_FALLBACK,
            error_code=code,
            ruleset_version=0 if loaded is None else loaded.ruleset.version,
            received_at=None if loaded is None else loaded.received_at,
            stage=self._stage,
        )

    def _mismatch(self, key: str, flag_type: str, requested: str) -> None:
        """Logs type_mismatch once per flag key."""
        with self._lock:
            if key in self._mismatched:
                return
            self._mismatched.add(key)
        self._log(
            logging.WARNING,
            _events.TYPE_MISMATCH,
            flag=key,
            flag_type=flag_type,
            requested_type="number" if requested == _INT64 else requested,
        )

    # -- state ------------------------------------------------------------

    @property
    def stage(self) -> Stage:
        """The current stage of the fallback chain."""
        return self._stage

    def status(self) -> Status:
        """The stage and the staleness of the held ruleset."""
        return Status(stage=self._stage, stale_since=self._stale_since)

    def ready(self, timeout: float | None = 5.0) -> Stage:
        """Waits for the result of the first fetch, success or failure, and returns the stage reached.

        Waits at most ``timeout`` seconds (forever for None) and never raises.
        The stage is ``fresh``, ``last_good`` from the store, or ``built_in``.
        Evaluation before ``ready`` is legal and follows the fallback chain.
        """
        self._ready.wait(timeout)
        return self._stage

    def on_change(self, listener: Callable[[list[str]], None]) -> Callable[[], None]:
        """Registers ``listener``, called with the sorted keys of the flags whose value may have changed.

        It runs synchronously on the delivery thread, so it must not call
        ``close``; one that raises is logged as ``listener_failed``. Returns the
        function that removes it.
        """
        return self._register(self._listeners, listener)

    def on_stage(self, listener: Callable[[Stage], None]) -> Callable[[], None]:
        """Registers ``listener``, called with the new stage on every transition of the fallback chain.

        It sees transitions after it is registered: read ``stage`` after
        registering for the current one. Like ``on_change`` listeners it runs on
        the delivery thread. Returns the function that removes it.
        """
        return self._register(self._stage_listeners, listener)

    def _register(self, registry: dict[int, Any], listener: Any) -> Callable[[], None]:
        with self._lock:
            listener_id = self._next_listener
            self._next_listener += 1
            registry[listener_id] = listener

        def remove() -> None:
            with self._lock:
                registry.pop(listener_id, None)

        return remove

    def close(self, timeout: float | None = 5.0) -> None:
        """Stops fetching and streaming, waiting at most ``timeout`` seconds for the thread to end.

        Getters keep serving the held ruleset. Idempotent.
        """
        self._closed = True
        self._delivery.stop(timeout)
        self._ready.set()

    # -- what the delivery thread drives ----------------------------------

    def _log(self, level: int, event: str, **fields: Any) -> None:
        # A logger that raises must not take the client down.
        with contextlib.suppress(Exception):
            self._logger(level, event, fields)

    def _held_version(self) -> int:
        loaded = self._loaded
        return 0 if loaded is None else loaded.ruleset.version

    def _decode(self, document: bytes) -> tuple[Ruleset, Mapping[str, Any]]:
        """Accepts a document only when it parses, has a supported schema_version and is of server scope."""
        try:
            raw = parse(document)
        except RulesetError as err:
            raise Rejected(CAUSE_PARSE, str(err)) from err
        if not isinstance(raw, dict):
            raise Rejected(CAUSE_PARSE, "ruleset: the document is not an object")
        schema_version = raw.get("schema_version")
        if schema_version is None:
            raise Rejected(CAUSE_PARSE, "ruleset: no schema_version")
        if type(schema_version) is not int:
            raise Rejected(CAUSE_PARSE, "ruleset: schema_version is not an integer")
        if schema_version != SCHEMA_VERSION:
            self._log_schema_unsupported(schema_version)
            raise Rejected(
                CAUSE_SCHEMA_UNSUPPORTED,
                f"ruleset: schema_version {schema_version}, this SDK reads {SCHEMA_VERSION}",
            )
        if raw.get("scope") != "server":
            raise Rejected(CAUSE_WRONG_SCOPE, f"ruleset: scope {raw.get('scope')!r}, want 'server'")
        try:
            return decode(raw), raw
        except RulesetError as err:
            raise Rejected(CAUSE_PARSE, str(err)) from err

    def _log_schema_unsupported(self, schema_version: int) -> None:
        if self._schema_logged:
            return
        self._schema_logged = True
        self._log(
            logging.ERROR,
            _events.SCHEMA_UNSUPPORTED,
            schema_version=schema_version,
            supported=[SCHEMA_VERSION],
        )

    def _swap(
        self, ruleset: Ruleset, raw: Mapping[str, Any], source: str, received_at: datetime
    ) -> list[str]:
        """Makes ``ruleset`` the held one; returns the keys of the flags whose evaluation may have changed."""
        previous = self._loaded
        self._loaded = _Loaded(ruleset, raw, received_at)
        self._log(
            logging.INFO,
            _events.RULESET_LOADED,
            source=source,
            ruleset_version=ruleset.version,
            flags=len(ruleset.flags),
        )
        self._report_unsupported(ruleset)
        return changed_flags(None if previous is None else previous.document, raw)

    def _report_unsupported(self, ruleset: Ruleset) -> None:
        """Logs rule_unsupported for every flag holding a construct the evaluator does not know.

        Once per flag and ruleset version.

        The evaluator finds them when it decodes, so a flag nobody evaluates is
        reported too, and an SDK that must be upgraded says so without a line
        per evaluation.
        """
        for key, constructs in sorted(ruleset.unsupported_rules().items()):
            if self._unsupported_logged.get(key) == ruleset.version:
                continue
            self._unsupported_logged[key] = ruleset.version
            self._log(
                logging.ERROR,
                _events.RULE_UNSUPPORTED,
                flag=key,
                constructs=[f"{c.kind} {c.name}" for c in constructs],
                ruleset_version=ruleset.version,
            )

    def _load_store(self, path: Path) -> None:
        """Seeds the client with the ruleset persisted at ``path`` as last_good.

        An absent file is a first start; an unreadable or invalid one logs
        store_failed and leaves the client at built_in.
        """
        try:
            found = _store.read(path)
            if found is None:
                return
            document, written = found
            ruleset, raw = self._decode(document)
        except (OSError, Rejected) as err:
            self._log(logging.WARNING, _events.STORE_FAILED, path=str(path), op="load", cause=str(err))
            return
        # The file was written when the ruleset was received, so its age shows through received_at.
        self._swap(ruleset, raw, "store", datetime.fromtimestamp(written, UTC))
        self._set_stage(Stage.LAST_GOOD)

    def _persist(self, document: bytes) -> None:
        if self._store_path is None:
            return
        try:
            _store.write(self._store_path, document)
        except OSError as err:
            self._log(
                logging.WARNING, _events.STORE_FAILED, path=str(self._store_path), op="save", cause=str(err)
            )

    def _set_stage(self, stage: Stage) -> None:
        """Moves the fallback chain to ``stage``, reporting a transition to the log and on_stage listeners."""
        previous = self._stage
        if previous == stage:
            return
        self._stage = stage
        self._log(
            logging.INFO if stage == Stage.FRESH else logging.WARNING,
            _events.STAGE_CHANGED,
            **{"from": previous.value, "to": stage.value},
        )
        with self._lock:
            listeners = [self._stage_listeners[i] for i in sorted(self._stage_listeners)]
        for listener in listeners:
            self._call(listener, stage)

    def _fire_change(self, changed: list[str]) -> None:
        if not changed:
            return
        with self._lock:
            listeners = [self._listeners[i] for i in sorted(self._listeners)]
        for listener in listeners:
            self._call(listener, list(changed))

    def _call(self, listener: Callable[[Any], None], argument: Any) -> None:
        try:
            listener(argument)
        except Exception as err:
            self._log(logging.ERROR, _events.LISTENER_FAILED, cause=repr(err))


class _Host:
    """The delivery thread's view of the client."""

    def __init__(self, client: Client) -> None:
        self._client = client

    def held_version(self) -> int:
        return self._client._held_version()

    def accept(self, document: bytes) -> list[str]:
        client = self._client
        ruleset, raw = client._decode(document)
        held = client._loaded
        if held is not None and ruleset.version < held.ruleset.version:
            raise Rejected(
                CAUSE_VERSION_DOWNGRADE,
                f"version {ruleset.version} is below the held {held.ruleset.version}",
            )
        changed = client._swap(ruleset, raw, "server", datetime.now(UTC))
        client._persist(document)
        return changed

    def fetch_succeeded(self, changed: list[str]) -> None:
        client = self._client
        client._revoked = False
        client._stale_since = None
        client._set_stage(Stage.FRESH)
        client._fire_change(changed)

    def fetch_failed(self, cause: str, status: int, error: str) -> None:
        client = self._client
        fields: dict[str, Any] = {"cause": cause, "error": error}
        if status:
            fields["status"] = status
        client._log(logging.WARNING, _events.FETCH_FAILED, **fields)
        if client._stale_since is None:
            client._stale_since = datetime.now(UTC)
        if client._stage == Stage.FRESH:
            client._set_stage(Stage.LAST_GOOD)

    def key_revoked(self, status: int) -> None:
        """Logs key_revoked once, however many of the fetch loop and the change stream see the refusal."""
        client = self._client
        if not client._revoked:
            client._revoked = True
            client._log(logging.ERROR, _events.KEY_REVOKED, status=status)

    def log(self, level: int, event: str, **fields: Any) -> None:
        self._client._log(level, event, **fields)

    def mark_ready(self) -> None:
        self._client._ready.set()


class _Invalid:
    """Marks a served value that does not fit its flag's type."""


_INVALID: Any = _Invalid()

_INT64_MIN = -(1 << 63)
_INT64_MAX = (1 << 63) - 1


def _served_value(flag_type: str, value: Any) -> Any:
    """The value a getter hands out for a flag of ``flag_type``, or _INVALID when it does not fit the type.

    A number is an IEEE 754 double; JSON is copied, since the ruleset is shared
    and must never be mutated through a served value.
    """
    if flag_type == _BOOLEAN:
        return value if isinstance(value, bool) else _INVALID
    if flag_type == _STRING:
        return value if isinstance(value, str) else _INVALID
    if flag_type == _NUMBER:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            return _INVALID
        try:
            return float(value)
        except OverflowError:
            return _INVALID
    return copy.deepcopy(value)


def _as_int64(value: float) -> Any:
    """``value`` as an int when it is whole and fits 64 bits, else _INVALID."""
    if not math.isfinite(value) or value != math.trunc(value) or not _INT64_MIN <= value <= _INT64_MAX:
        return _INVALID
    return int(value)
