"""The client: getters over an immutable decoded ruleset, kept fresh by the delivery thread."""

from __future__ import annotations

import asyncio
import contextlib
import copy
import json
import logging
import math
import os
import platform as platform_module
import threading
import time
import weakref
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, TypeAlias
from urllib.parse import urlsplit

from . import _events, _store
from ._changes import changed_flags
from ._delivery import (
    CAUSE_PARSE,
    CAUSE_SCHEMA_UNSUPPORTED,
    CAUSE_VERSION_DOWNGRADE,
    CAUSE_WRONG_SCOPE,
    DEFAULT_DOWN_POLL_INTERVAL,
    DEFAULT_REQUEST_TIMEOUT,
    Delivery,
    Rejected,
    Waits,
    user_agent,
)
from ._detail import Detail, Experiment, Stage, Status
from ._eval import SCHEMA_VERSION, Context, Ruleset, RulesetError, decode, evaluate, parse
from ._events import Logger, standard_logger
from ._ingest import Ingest, event_id
from ._overrides import LocalOverrides, Override
from ._reporting import (
    EventQueue,
    Exposure,
    ExposureSuppressor,
    Recorder,
    Reporter,
    Served,
    ServedDetails,
    Summaries,
    Timing,
    alias_of,
    fingerprint,
)
from ._version import __version__

#: A ruleset document as the ruleset endpoint serves it: JSON text, its UTF-8 bytes, or the parsed object.
Document: TypeAlias = bytes | str | Mapping[str, Any]

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
_REASON_LOCAL_OVERRIDE = "local_override"
_REASON_OVERRIDE = "override"

# Defaults of reporting, in seconds.
DEFAULT_FLUSH_INTERVAL = 2.0
DEFAULT_SUMMARY_INTERVAL = 60.0
DEFAULT_CLOSE_TIMEOUT = 5.0
_MAX_WINDOW_AGE = 3600.0
# The most experiment assignments the exposure suppressor holds, and the most flag and context pairs
# an exposure call can still look up.
_EXPOSURE_MEMORY = 10_000
_DETAIL_MEMORY = 10_000


@dataclass(frozen=True, slots=True)
class _Online:
    """What a client that talks to a server was built with, kept to restart its thread after a fork."""

    sdk_key: str
    base_url: str
    waits: Waits
    stream: bool
    flush_interval: float
    summary_interval: float
    max_window_age: float


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
        ruleset stream is connected; default 300, at least 30. While the stream
        is down or disabled the safety poll runs every 30 seconds instead, so
        a kill switch is never further away than that.
    ``stream``
        Whether to hold the ruleset stream; on by default. It carries nothing
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
    ``send_summaries``
        Whether the evaluation summary is reported: on by default. It counts
        served values per flag, variation and reason, never a unit ID or a
        value, so the console can show which flags are evaluated and which are
        stale.
    ``summary_interval``
        Seconds between evaluation summaries; default 60.
    ``send_exposures``
        Whether exposures and aliases are sent: on by default. Off logs
        ``exposures_off`` at construction.
    ``flush_interval``
        Seconds between sends of the queued exposures and aliases; default 2.
    ``platform``
        Reported in the User-Agent, in the events envelope and on every
        exposure whose context names no ``platform`` attribute; default the
        operating system, as ``platform.system()`` names it in lower case.
    ``app_version``
        Reported with the evaluation summary and on every exposure whose
        context names no ``app_version`` attribute.
    ``allow_overrides``
        Enables ``local_overrides``; logs ``local_overrides_allowed`` at
        construction. A protected environment refuses them all the same.
    ``offline``
        A ruleset document, exactly as the ruleset endpoint serves it, or a
        function returning one, called at construction (and by a test
        client's ``reload``). The client makes no network request at all and
        starts no thread: ``sdk_key`` and ``base_url`` are ignored, the stage
        is ``fresh``, and what it would report goes to ``recorder``. A
        document that does not decode raises ``ValueError``.

    A pre-forking server (gunicorn, uWSGI) needs no hook: a client forked into
    a worker restarts its thread there, refetches, and reports the worker's
    own events; what the parent had queued stays the parent's to send.
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
        send_summaries: bool = True,
        summary_interval: float = DEFAULT_SUMMARY_INTERVAL,
        send_exposures: bool = True,
        flush_interval: float = DEFAULT_FLUSH_INTERVAL,
        platform: str | None = None,
        app_version: str | None = None,
        allow_overrides: bool = False,
        offline: Document | Callable[[], Document] | None = None,
        _time_divisor: float = 1.0,
        _down_poll_interval: float = DEFAULT_DOWN_POLL_INTERVAL,
    ) -> None:
        if request_timeout <= 0:
            raise ValueError("flagnetic: request_timeout must be positive")
        if poll_interval is not None and poll_interval <= 0:
            raise ValueError("flagnetic: poll_interval must be positive")
        if summary_interval <= 0:
            raise ValueError("flagnetic: summary_interval must be positive")
        if flush_interval <= 0:
            raise ValueError("flagnetic: flush_interval must be positive")

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
        self._platform = platform if platform is not None else _system()
        self._app_version = app_version or ""
        self._send_summaries = send_summaries
        self._send_exposures = send_exposures
        self._exposed = ExposureSuppressor(_EXPOSURE_MEMORY)
        self._served = ServedDetails(_DETAIL_MEMORY)
        self._overrides = LocalOverrides(allow_overrides, self._held_ruleset, self._log, self._fire_change)
        self._recorder: Recorder | None = None
        self._summaries: Summaries | None = None
        self._queue: EventQueue | None = None
        self._delivery: Delivery | None = None
        self._reporter: Reporter | None = None
        self._source: Callable[[], Document] | None = None
        self._store_path: Path | None = None
        if allow_overrides:
            self._log(logging.WARNING, _events.LOCAL_OVERRIDES_ALLOWED)
        if not send_exposures:
            self._log(logging.WARNING, _events.EXPOSURES_OFF)

        if offline is not None:
            self._source = offline if callable(offline) else (lambda: offline)
            self._recorder = Recorder()
            self._load_offline()
            return

        if not isinstance(sdk_key, str) or not sdk_key:
            raise ValueError("flagnetic: the SDK key is empty")
        if sdk_key.startswith(_CLIENT_KEY_PREFIX):
            raise ValueError(
                "flagnetic: a client SDK key cannot fetch the ruleset; the Python SDK needs a server key"
            )
        parts = urlsplit(base_url)
        if parts.scheme not in ("http", "https") or not parts.hostname:
            raise ValueError(f"flagnetic: base_url {base_url!r} is not an http or https URL")

        self._store_path = Path(store) if store is not None else None
        if self._store_path is not None:
            self._load_store(self._store_path)
        self._online = _Online(
            sdk_key=sdk_key,
            base_url=base_url,
            waits=Waits.of(request_timeout, poll_interval, _time_divisor, _down_poll_interval),
            stream=stream,
            flush_interval=flush_interval / _time_divisor,
            summary_interval=summary_interval / _time_divisor,
            max_window_age=_MAX_WINDOW_AGE / _time_divisor,
        )
        self._start()
        _live.add(self)

    def _start(self) -> None:
        """Builds the reporter and the delivery thread, and starts the thread."""
        online = self._online
        waits = online.waits
        timing = Timing(
            flush_interval=online.flush_interval,
            summary_interval=online.summary_interval,
            max_window_age=online.max_window_age,
            retry_base=waits.retry_base,
            retry_cap=waits.retry_cap,
            unauthorized_cap=waits.unauthorized_cap,
        )
        agent = user_agent(__version__, self._platform)
        ingest = Ingest(
            base_url=online.base_url,
            sdk_key=online.sdk_key,
            agent=agent,
            platform=self._platform,
            version=__version__,
            schema_version=SCHEMA_VERSION,
            request_timeout=waits.request_timeout,
        )
        now = time.monotonic()
        delivery: Delivery | None = None

        def wake() -> None:
            if delivery is not None:
                delivery.wake()

        self._queue = EventQueue(timing, ingest, self._log, wake, now) if self._send_exposures else None
        self._summaries = (
            Summaries(
                timing,
                ingest,
                self._log,
                now,
                ruleset_version=self._held_version,
                app_version=self._app_version,
            )
            if self._send_summaries
            else None
        )
        reporter = (
            Reporter(self._queue, self._summaries, self._log)
            if self._queue is not None or self._summaries is not None
            else None
        )
        self._reporter = reporter
        delivery = Delivery(
            _Host(self),
            base_url=online.base_url,
            sdk_key=online.sdk_key,
            waits=waits,
            stream=online.stream,
            agent=agent,
            schema_version=SCHEMA_VERSION,
            reporter=reporter,
        )
        self._delivery = delivery
        delivery.start()

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
        """The API boundary every getter goes through: it never raises, and counts what it served."""
        loaded = self._loaded
        try:
            detail = self._detail(loaded, context, key, requested, default)
        except Exception as err:
            self._log(logging.ERROR, _events.EVALUATION_FAILED, flag=key, cause=repr(err))
            detail = self._fallback(loaded, default, _ERROR_GENERAL)
        try:
            self._count(key, detail.variation, detail.reason)
            if self._send_exposures and loaded is not None:
                self._served_detail(loaded, context, key, detail)
        except Exception as err:
            self._log(logging.ERROR, _events.EVALUATION_FAILED, operation="report", flag=key, cause=repr(err))
        return detail

    def _count(self, flag: str, variation: str, reason: str) -> None:
        """Counts one served value in the evaluation summary, or in the recorder of an offline client."""
        if self._recorder is not None:
            self._recorder.count(flag, variation, reason)
        elif self._summaries is not None:
            self._summaries.count(flag, variation, reason)

    def _served_detail(self, loaded: _Loaded, context: Context, key: str, detail: Detail) -> None:
        """Remembers the assignment a detail served, and records the exposure of an automatic flag.

        What an exposure call reports is what was served, not what the ruleset
        says by the time it is called; a flag that stops carrying an experiment
        block forgets what it served, so the call reports nothing rather than
        an assignment the ruleset no longer serves.
        """
        experiment = detail.experiment
        if experiment is None:
            if self._served.ever_carried(key):
                self._served.forget(key, _fingerprint(context), detail.ruleset_version)
            return
        self._served.remember(
            key, _fingerprint(context), Served(experiment, detail.reason, detail.ruleset_version)
        )
        # Automatic exposure is the flag's own setting: every evaluation carrying an experiment
        # block records one, as if exposure had been called.
        flag = loaded.ruleset.flag(key)
        if flag is not None and flag.automatic_exposure:
            self._emit(context, key, detail.reason, detail.ruleset_version, experiment)

    def _detail(
        self, loaded: _Loaded | None, context: Context, key: str, requested: str, default: Any
    ) -> Detail:
        flag_type = _NUMBER if requested == _INT64 else requested
        local = self._overrides.by_key.get(key) if self._overrides.by_key else None
        if local is not None and not (loaded is not None and loaded.ruleset.protected):
            return self._local(loaded, key, local, requested, default)
        if loaded is None:
            return self._fallback(None, default, _ERROR_NO_RULESET)
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

    def _local(
        self, loaded: _Loaded | None, key: str, local: Override, requested: str, default: Any
    ) -> Detail:
        """Serves a local override, which follows the getter's type rules like a flag's value does."""
        flag_type = _NUMBER if requested == _INT64 else requested
        if local.type != flag_type:
            self._mismatch(key, local.type, requested)
            return self._fallback(loaded, default, _ERROR_TYPE_MISMATCH)
        served = _served_value(flag_type, local.value)
        if requested == _INT64:
            served = _as_int64(served)
            if served is _INVALID:
                self._mismatch(key, _NUMBER, requested)
                return self._fallback(loaded, default, _ERROR_TYPE_MISMATCH)
        return Detail(
            value=served,
            reason=_REASON_LOCAL_OVERRIDE,
            ruleset_version=0 if loaded is None else loaded.ruleset.version,
            received_at=None if loaded is None else loaded.received_at,
            stage=self._stage,
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

    @property
    def local_overrides(self) -> LocalOverrides:
        """This client's local overrides; ``set`` raises ``LocalOverridesNotAllowed`` without the option."""
        return self._overrides

    @property
    def recorder(self) -> Recorder | None:
        """What an offline client would have reported; None for a client that reports to a server."""
        return self._recorder

    # -- events -----------------------------------------------------------

    def exposure(self, context: Context, key: str) -> None:
        """Records that the unit of ``context`` encountered the behaviour the flag's value decides.

        What is reported is the most recent detail this client served for that
        flag and that context, when it carried an experiment block (an override
        landing in a running experiment included): the assignment the
        application acted on is the one analysis counts, whatever ruleset has
        arrived since. A local override, a fallback and a flag not evaluated
        for this context record nothing and log ``exposure_skipped`` at debug
        level. Repeats of one assignment are sent once by this process; the
        server's deduplication and its first exposure stay authoritative.
        Never raises.
        """
        try:
            if not self._send_exposures:
                return
            if key in self._overrides.by_key and not self._protected():
                self._log(logging.DEBUG, _events.EXPOSURE_SKIPPED, flag=key, cause="local_override")
                return
            served = self._served.recall(key, _fingerprint(context))
            if served is None:
                cause = "no_detail" if self._served.ever_carried(key) else "no_experiment"
                self._log(logging.DEBUG, _events.EXPOSURE_SKIPPED, flag=key, cause=cause)
                return
            self._emit(context, key, served.reason, served.ruleset_version, served.experiment)
        except Exception as err:
            self._log(
                logging.ERROR, _events.EVALUATION_FAILED, operation="exposure", flag=key, cause=repr(err)
            )

    def alias(self, from_unit_type: str, from_unit_id: str, to_unit_type: str, to_unit_id: str) -> None:
        """Records an alias linking one unit to another, typically an anonymous ID to a user ID at sign-up.

        It changes no context: keep the anonymous unit in the contexts you
        evaluate with. Never raises.
        """
        try:
            if self._send_exposures:
                self._record(alias_of(from_unit_type, from_unit_id, to_unit_type, to_unit_id))
        except Exception as err:
            self._log(logging.ERROR, _events.EVALUATION_FAILED, operation="alias", cause=repr(err))

    def _emit(
        self, context: Context, key: str, reason: str, ruleset_version: int, experiment: Experiment
    ) -> None:
        if not self._exposed.admit(
            experiment.key, experiment.unit_type, experiment.unit_id, experiment.variation
        ):
            return
        attributes = getattr(context, "attributes", None) or {}
        # Platform and app version come from the context's predeclared attributes, and from the
        # options where the context names neither.
        platform = attributes.get("platform")
        app_version = attributes.get("app_version")
        now = datetime.now(UTC)
        self._record(
            Exposure(
                event_id=event_id(now),
                occurred_at=now,
                experiment_key=experiment.key,
                flag_key=key,
                variation_key=experiment.variation,
                unit_type=experiment.unit_type,
                unit_id=experiment.unit_id,
                # The block comes with reason experiment, and with reason override when an
                # override's shadow evaluation lands in the experiment.
                evaluation_reason=_REASON_OVERRIDE if reason == _REASON_OVERRIDE else "experiment",
                override=experiment.override,
                ruleset_version=ruleset_version,
                platform=platform if isinstance(platform, str) else self._platform,
                app_version=app_version if isinstance(app_version, str) else self._app_version,
            )
        )

    def _record(self, event: Any) -> None:
        """Hands an exposure or an alias to the queue, or to the recorder of an offline client."""
        if self._recorder is not None:
            self._recorder.record(event)
        elif self._queue is not None:
            self._queue.record(event.wire())

    def flush(self, timeout: float | None = DEFAULT_CLOSE_TIMEOUT) -> None:
        """Reports the evaluation summary so far and sends the queued exposures and aliases.

        Waits at most ``timeout`` seconds for the server's answers; what is not
        sent by then goes on in the background, and a batch waiting out a
        backoff is left to it. Never raises; an offline client recorded
        everything at the call already.
        """
        reporter = self._reporter if self._recorder is None else None
        delivery = self._delivery
        if reporter is None or delivery is None or self._closed:
            return
        done = reporter.request_flush()
        delivery.wake()
        done.wait(timeout)

    def close(self, timeout: float | None = DEFAULT_CLOSE_TIMEOUT) -> None:
        """Stops fetching and streaming and reports what is queued one last time.

        Waits at most ``timeout`` seconds for the thread to end; a batch
        waiting out a backoff goes at once, and none goes again. Getters keep
        serving the held ruleset. Idempotent.
        """
        self._closed = True
        _live.discard(self)
        if self._delivery is not None:
            self._delivery.stop(timeout)
        self._ready.set()

    async def aready(self, timeout: float | None = 5.0) -> Stage:
        """``ready`` for asyncio: waits on a worker thread, so the event loop keeps running."""
        return await asyncio.to_thread(self.ready, timeout)

    async def aflush(self, timeout: float | None = DEFAULT_CLOSE_TIMEOUT) -> None:
        """``flush`` for asyncio: waits on a worker thread, so the event loop keeps running."""
        await asyncio.to_thread(self.flush, timeout)

    async def aclose(self, timeout: float | None = DEFAULT_CLOSE_TIMEOUT) -> None:
        """``close`` for asyncio: waits on a worker thread, so the event loop keeps running."""
        await asyncio.to_thread(self.close, timeout)

    # -- offline ----------------------------------------------------------

    def _load_offline(self) -> list[str]:
        """Reads the offline document and makes it the held ruleset at stage fresh."""
        assert self._source is not None
        try:
            document = _document_bytes(self._source())
            ruleset, raw = self._decode(document)
        except Rejected as err:
            raise ValueError(f"flagnetic: the offline document is not a ruleset: {err}") from err
        changed = self._swap(ruleset, raw, "offline", datetime.now(UTC))
        self._set_stage(Stage.FRESH)
        self._ready.set()
        return changed

    def _reload(self) -> None:
        """Reads the offline document again and fires the change listeners; keeps the held one on failure."""
        if self._source is None:
            raise RuntimeError("flagnetic: only an offline client reloads")
        self._fire_change(self._load_offline())

    # -- forks --------------------------------------------------------------

    def _after_fork(self) -> None:
        """Restarts the thread in a forked child, which holds the parent's ruleset but no thread.

        Every lock is made anew, since a thread of the parent may have held one
        at the fork. The parent's queued events and counts stay the parent's to
        send: a child that sent them too would report them twice.
        """
        if self._closed or self._delivery is None:
            return
        self._lock = threading.Lock()
        ready = self._ready.is_set()
        self._ready = threading.Event()
        if ready:
            self._ready.set()
        self._exposed = ExposureSuppressor(_EXPOSURE_MEMORY)
        self._served = ServedDetails(_DETAIL_MEMORY)
        self._overrides.after_fork()
        self._delivery.abandon()
        self._start()

    # -- what the delivery thread drives ----------------------------------

    def _log(self, level: int, event: str, **fields: Any) -> None:
        # A logger that raises must not take the client down.
        with contextlib.suppress(Exception):
            self._logger(level, event, fields)

    def _held_ruleset(self) -> Ruleset | None:
        loaded = self._loaded
        return None if loaded is None else loaded.ruleset

    def _protected(self) -> bool:
        loaded = self._loaded
        return loaded is not None and loaded.ruleset.protected

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
        changed = changed_flags(None if previous is None else previous.document, raw)
        dropped = self._overrides.refuse(ruleset)
        return sorted(set(changed) | set(dropped)) if dropped else changed

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
        """Logs key_revoked once, however many of the fetch loop and the ruleset stream see the refusal."""
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


def _fingerprint(context: Context) -> str:
    return fingerprint(getattr(context, "units", None) or {}, getattr(context, "attributes", None) or {})


def _document_bytes(document: Document) -> bytes:
    if isinstance(document, (bytes, bytearray)):
        return bytes(document)
    if isinstance(document, str):
        return document.encode()
    if isinstance(document, Mapping):
        return json.dumps(document).encode()
    raise TypeError(
        f"flagnetic: a ruleset document is bytes, str or a mapping, not {type(document).__name__}"
    )


def _system() -> str:
    return platform_module.system().lower() or "unknown"


# The clients that hold a thread, so that a forked child restarts theirs. Weak, so a client nobody
# holds any more is not kept alive for this.
_live: weakref.WeakSet[Client] = weakref.WeakSet()


def _after_fork_in_child() -> None:
    for client in list(_live):
        client._after_fork()


# A pre-forking server (gunicorn, uWSGI) forks after the application built its client; the child
# inherits the client but not its thread. Windows has no fork.
if hasattr(os, "register_at_fork"):
    os.register_at_fork(after_in_child=_after_fork_in_child)
