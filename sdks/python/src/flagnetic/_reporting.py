"""What a client reports: evaluation summaries, exposures and aliases.

Getters, ``exposure`` and ``alias`` record from any thread; the delivery
thread sends, between fetches, when the reporter's next deadline comes or a
flush asks. Every event is given its event ID when it is recorded and keeps it
for every retry, so the server drops a resend as a duplicate.
"""

from __future__ import annotations

import json
import logging
import threading
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any

from . import _events
from ._delivery import backoff_ceiling, full_jitter
from ._ingest import MAX_SUMMARY_ROWS, Event, Ingest, Outcome, Sent, event_id, iso

Log = Callable[..., None]


@dataclass(frozen=True, slots=True)
class SummaryCount:
    """One row of an evaluation summary: how often a flag served a variation for a reason.

    It carries no unit ID and no value; ``variation`` is empty for ``fallback``
    and ``local_override``.
    """

    flag: str
    variation: str
    reason: str
    count: int


@dataclass(frozen=True, slots=True)
class Exposure:
    """A unit encountered behaviour that depends on its experiment assignment.

    ``evaluation_reason`` is ``experiment``, or ``override`` when an override
    decided the variation; ``override`` marks an exposure an override
    influenced, which analysis counts apart. ``platform`` and ``app_version``
    are the context's attributes, else the client's options, else empty.
    """

    event_id: str
    occurred_at: datetime
    experiment_key: str
    flag_key: str
    variation_key: str
    unit_type: str
    unit_id: str
    evaluation_reason: str
    override: bool
    ruleset_version: int
    platform: str = ""
    app_version: str = ""

    def wire(self) -> dict[str, Any]:
        out: dict[str, Any] = {
            "type": "exposure",
            "event_id": self.event_id,
            "occurred_at": iso(self.occurred_at),
            "experiment_key": self.experiment_key,
            "flag_key": self.flag_key,
            "variation_key": self.variation_key,
            "unit_type": self.unit_type,
            "unit_id": self.unit_id,
            "evaluation_reason": self.evaluation_reason,
            "ruleset_version": self.ruleset_version,
            "override": self.override,
        }
        if self.platform:
            out["platform"] = self.platform
        if self.app_version:
            out["app_version"] = self.app_version
        return out


@dataclass(frozen=True, slots=True)
class Alias:
    """Links one unit to another, typically an anonymous ID to a user ID at sign-up."""

    event_id: str
    occurred_at: datetime
    from_unit_type: str
    from_unit_id: str
    to_unit_type: str
    to_unit_id: str

    def wire(self) -> dict[str, Any]:
        return {
            "type": "alias",
            "event_id": self.event_id,
            "occurred_at": iso(self.occurred_at),
            "from_unit_type": self.from_unit_type,
            "from_unit_id": self.from_unit_id,
            "to_unit_type": self.to_unit_type,
            "to_unit_id": self.to_unit_id,
        }


def alias_of(from_unit_type: str, from_unit_id: str, to_unit_type: str, to_unit_id: str) -> Alias:
    now = datetime.now(UTC)
    return Alias(event_id(now), now, from_unit_type, from_unit_id, to_unit_type, to_unit_id)


_Row = tuple[str, str, str]


def _rows(counts: Mapping[_Row, int]) -> list[SummaryCount]:
    return [SummaryCount(f, v, r, n) for (f, v, r), n in sorted(counts.items())]


class Recorder:
    """What an offline client would have reported, held in memory for a test to assert on.

    The evaluation counts since construction or the last ``reset``, and the
    exposures and aliases recorded at the call itself. Repeats of one
    assignment are suppressed before they reach it, exactly as they are online.
    Nothing here reaches the network. Safe for concurrent use.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._counts: dict[_Row, int] = {}
        self._events: list[Exposure | Alias] = []

    def count(self, flag: str, variation: str, reason: str) -> None:
        row = (flag, variation, reason)
        with self._lock:
            self._counts[row] = self._counts.get(row, 0) + 1

    def record(self, event: Exposure | Alias) -> None:
        with self._lock:
            self._events.append(event)

    def summary(self) -> list[SummaryCount]:
        """The evaluation counts, ordered by flag, variation and reason."""
        with self._lock:
            return _rows(self._counts)

    def exposures(self) -> list[Exposure]:
        """The exposures recorded, oldest first."""
        with self._lock:
            return [e for e in self._events if isinstance(e, Exposure)]

    def aliases(self) -> list[Alias]:
        """The aliases recorded, oldest first."""
        with self._lock:
            return [e for e in self._events if isinstance(e, Alias)]

    def reset(self) -> None:
        """Forgets everything recorded."""
        with self._lock:
            self._counts = {}
            self._events = []


class ExposureSuppressor:
    """The bounded set that suppresses repeats of one assignment within this process.

    At capacity the oldest assignment is forgotten rather than all of them; the
    server's deduplication by event ID and its first exposure stay
    authoritative, so a forgotten one costs at most a duplicate it drops.
    """

    def __init__(self, capacity: int) -> None:
        self._capacity = capacity
        self._seen: dict[tuple[str, str, str, str], None] = {}
        self._lock = threading.Lock()

    def admit(self, experiment: str, unit_type: str, unit_id: str, variation: str) -> bool:
        """True the first time this experiment, unit and variation are seen."""
        key = (experiment, unit_type, unit_id, variation)
        with self._lock:
            if key in self._seen:
                return False
            while len(self._seen) >= self._capacity:
                # A dict iterates in insertion order, so the first key is the oldest.
                del self._seen[next(iter(self._seen))]
            self._seen[key] = None
            return True


@dataclass(frozen=True, slots=True)
class Served:
    """The assignment an evaluation served, which is what an exposure call reports."""

    experiment: Any
    reason: str
    ruleset_version: int


def fingerprint(units: Mapping[str, Any], attributes: Mapping[str, Any]) -> str:
    """A context's units and attributes: an exposure call finds the evaluation made for the same context."""
    return json.dumps([sorted(units.items()), sorted(attributes.items())], default=str)


class ServedDetails:
    """The assignment every flag last served, by flag and context: what an exposure call reports.

    Reporting a fresh evaluation instead would report an assignment the
    application never served when a ruleset arrives between the evaluation and
    the call. The memory is bounded and forgets its oldest entry first; a
    forgotten one costs an exposure the application asked for, so the bound is
    generous.
    """

    def __init__(self, capacity: int) -> None:
        self._capacity = capacity
        self._held: dict[tuple[str, str], Served] = {}
        # The flags an evaluation of which has ever carried an experiment block.
        self._carried: set[str] = set()
        self._lock = threading.Lock()

    def ever_carried(self, flag: str) -> bool:
        """Whether an evaluation of the flag has ever carried an experiment block.

        It tells a flag with no running experiment (or a wrong key) from a call
        passing a context the flag was not evaluated with.
        """
        return flag in self._carried

    def remember(self, flag: str, context: str, served: Served) -> None:
        """Remembers what the flag served for the context, unless a later ruleset's is held."""
        key = (flag, context)
        with self._lock:
            self._carried.add(flag)
            previous = self._held.get(key)
            if previous is not None and previous.ruleset_version > served.ruleset_version:
                return
            self._held.pop(key, None)
            while len(self._held) >= self._capacity:
                del self._held[next(iter(self._held))]
            self._held[key] = served

    def forget(self, flag: str, context: str, ruleset_version: int) -> None:
        """Forgets what the flag served for the context, which no longer carries an experiment block."""
        key = (flag, context)
        with self._lock:
            previous = self._held.get(key)
            if previous is not None and previous.ruleset_version <= ruleset_version:
                del self._held[key]

    def recall(self, flag: str, context: str) -> Served | None:
        with self._lock:
            return self._held.get((flag, context))


@dataclass(frozen=True, slots=True)
class Timing:
    """The intervals and backoffs of reporting, in seconds."""

    flush_interval: float
    summary_interval: float
    # How old the interval of a window whose report failed may be before the window is dropped
    # rather than merged forward, so a long outage does not deliver counts nobody can place.
    max_window_age: float
    retry_base: float
    retry_cap: float
    unauthorized_cap: float
    capacity: int = 10_000
    batch_size: int = 500


class EventQueue:
    """The bounded in-memory queue of exposures and aliases.

    ``record`` appends from any thread and drops the oldest events when the
    queue is full; the delivery thread sends it every flush interval, at once
    when it reaches a batch, and on a flush. A batch that failed for a reason
    worth retrying keeps its event IDs and goes again after a backoff with full
    jitter; a flush while it waits leaves it to its backoff.
    """

    def __init__(
        self, timing: Timing, ingest: Ingest, log: Log, wake: Callable[[], None], now: float
    ) -> None:
        self._timing = timing
        self._ingest = ingest
        self._log = log
        self._wake = wake
        self._lock = threading.Lock()
        self._queue: list[Event] = []
        self._next_flush = now + timing.flush_interval
        self._retry_at: float | None = None
        self._failures = 0
        self._full = False

    def record(self, event: Event) -> None:
        with self._lock:
            self._queue.append(event)
            over = len(self._queue) - self._timing.capacity
            if over > 0:
                del self._queue[:over]
            full = len(self._queue) >= self._timing.batch_size and not self._full
            self._full = self._full or full
        if over > 0:
            self._log(
                logging.WARNING,
                _events.QUEUE_DROPPED,
                cause="queue_full",
                dropped=over,
                capacity=self._timing.capacity,
            )
        if full:
            self._wake()

    def deadline(self) -> float:
        if self._retry_at is not None:
            return self._retry_at
        return 0.0 if self._full else self._next_flush

    def run(self, now: float, *, flush: bool = False, final: bool = False) -> None:
        """Sends what is due: on the interval, a full batch, a flush, or one last time at close."""
        retrying = self._retry_at is not None
        if retrying and now < self._retry_at and not final:  # type: ignore[operator]
            return
        due = final or flush or self._full or retrying or now >= self._next_flush
        if now >= self._next_flush:
            self._next_flush = now + self._timing.flush_interval
        if not due:
            return
        self._retry_at = None
        self._send(now, final=final)

    def _send(self, now: float, *, final: bool) -> None:
        while True:
            with self._lock:
                self._full = False
                if not self._queue:
                    return
                batch = self._ingest.pack(self._queue[: self._timing.batch_size])[0]
            sent = self._ingest.post(batch)
            if sent.outcome in (Outcome.ACCEPTED, Outcome.DROPPED):
                # Rejected events go with the accepted ones: the server will never take them.
                ids = {e["event_id"] for e in batch}
                with self._lock:
                    self._queue = [e for e in self._queue if e["event_id"] not in ids]
                self._failures = 0
                self._dropped(sent, len(batch))
                continue
            if final:
                return
            self._failures += 1
            cap = (
                self._timing.unauthorized_cap
                if sent.outcome == Outcome.UNAUTHORIZED
                else self._timing.retry_cap
            )
            # The queue backs off on its own rather than honouring Retry-After: a rate limit whose
            # header names minutes would otherwise hold exposures back for minutes, and the
            # protocol allows either.
            wait = full_jitter(backoff_ceiling(self._failures, self._timing.retry_base, cap))
            fields: dict[str, Any] = {"cause": sent.cause, "events": len(batch), "retry_in": wait}
            if sent.status:
                fields["status"] = sent.status
            self._log(logging.WARNING, _events.EVENTS_FAILED, **fields)
            self._retry_at = now + wait
            return

    def _dropped(self, sent: Sent, events: int) -> None:
        if sent.outcome == Outcome.DROPPED:
            self._log(
                logging.WARNING,
                _events.QUEUE_DROPPED,
                cause="client_error",
                status=sent.status,
                events=events,
            )
        elif sent.rejected:
            self._log(
                logging.WARNING,
                _events.QUEUE_DROPPED,
                cause="refused_by_the_server",
                rejected=sent.rejected,
                accepted=sent.accepted,
                duplicates=sent.duplicates,
            )


@dataclass(slots=True)
class _Window:
    """What was evaluated over one interval."""

    start: datetime
    end: datetime
    counts: dict[_Row, int] = field(default_factory=dict)

    def merge(self, other: _Window) -> None:
        self.start = min(self.start, other.start)
        self.end = max(self.end, other.end)
        for row, n in other.counts.items():
            self.counts[row] = self.counts.get(row, 0) + n


class Summaries:
    """The evaluation summary: counts per flag, variation and reason over each interval.

    No unit ID, attribute or value is in it by construction: the counter only
    ever sees a flag key, a variation key and a reason. A report is retried
    whole, with the same event IDs, until the server answers it or the next
    interval is due; what was not delivered merges into the next window.
    """

    def __init__(
        self,
        timing: Timing,
        ingest: Ingest,
        log: Log,
        now: float,
        *,
        ruleset_version: Callable[[], int],
        app_version: str,
    ) -> None:
        self._timing = timing
        self._ingest = ingest
        self._log = log
        self._ruleset_version = ruleset_version
        self._app_version = app_version
        self._lock = threading.Lock()
        self._counts: dict[_Row, int] = {}
        self._start = datetime.now(UTC)
        self._next_report = now + timing.summary_interval
        # A window whose report failed, merged into the next one.
        self._kept: _Window | None = None
        # The report in progress: its window and the events not yet delivered, with their IDs.
        self._report: _Window | None = None
        self._pending: list[Event] = []
        self._retry_at = 0.0
        self._give_up_at = 0.0
        self._failures = 0
        # When a reporter whose key was refused may post again, and the backoff that grows it.
        self._not_before = 0.0
        self._refusals = 0

    def count(self, flag: str, variation: str, reason: str) -> None:
        row = (flag, variation, reason)
        with self._lock:
            self._counts[row] = self._counts.get(row, 0) + 1

    def deadline(self) -> float:
        return self._retry_at if self._pending else self._next_report

    def run(self, now: float, *, flush: bool = False, final: bool = False) -> None:
        # At close, a report in progress the server did not take ends it: posting the newer rows
        # after it would only wait out another failure.
        if (
            self._pending
            and (now >= self._retry_at or final)
            and not self._attempt(now, final=final)
            and final
        ):
            return
        if not (now >= self._next_report or flush or final):
            return
        self._next_report = now + self._timing.summary_interval
        window = self._take()
        if window is None:
            return
        if self._pending or now < self._not_before:
            # A report still retrying, or a key refused whose backoff has not run out: the rows
            # wait in the window rather than being posted over them.
            self._keep(window)
            return
        self._start_report(window, now)
        self._attempt(now, final=final)

    def _take(self) -> _Window | None:
        """Closes the current window, merging in the one a failed report kept unless it is too old."""
        end = datetime.now(UTC)
        with self._lock:
            window = _Window(self._start, end, self._counts)
            self._counts = {}
            self._start = end
        kept, self._kept = self._kept, None
        if kept is not None:
            age = (end - kept.start).total_seconds()
            if age > self._timing.max_window_age:
                self._log(
                    logging.WARNING,
                    _events.QUEUE_DROPPED,
                    kind="evaluation_summary",
                    cause="window_too_old",
                    rows=len(kept.counts),
                )
            else:
                window.merge(kept)
        return window if window.counts else None

    def _keep(self, window: _Window) -> None:
        if self._kept is None:
            self._kept = window
        else:
            self._kept.merge(window)

    def _start_report(self, window: _Window, now: float) -> None:
        rows = [
            {"flag": f, "variation": v or None, "reason": r, "count": n}
            for (f, v, r), n in sorted(window.counts.items())
        ]
        at = datetime.now(UTC)
        events: list[Event] = []
        for i in range(0, len(rows), MAX_SUMMARY_ROWS):
            event: dict[str, Any] = {
                "type": "evaluation_summary",
                "event_id": event_id(at),
                "interval_start": iso(window.start),
                "interval_end": iso(window.end),
                "ruleset_version": self._ruleset_version(),
                "counts": rows[i : i + MAX_SUMMARY_ROWS],
            }
            if self._app_version:
                event["app_version"] = self._app_version
            events.append(event)
        self._report = window
        self._pending = events
        self._failures = 0
        self._give_up_at = now + self._timing.summary_interval

    def _attempt(self, now: float, *, final: bool) -> bool:
        """Sends the report's batches in order until one fails; whether all of them went."""
        while self._pending:
            batch = self._ingest.pack(self._pending)[0]
            sent = self._ingest.post(batch)
            if sent.outcome in (Outcome.ACCEPTED, Outcome.DROPPED):
                self._pending = self._pending[len(batch) :]
                self._refusals, self._not_before = 0, 0.0
                self._delivered(sent, len(batch))
                continue
            if sent.outcome == Outcome.UNAUTHORIZED:
                # A refused key ends when an operator restores it, so the reporter waits at a
                # backoff growing to the refused key's cap rather than posting every interval.
                self._refusals += 1
                self._not_before = now + full_jitter(
                    backoff_ceiling(self._refusals, self._timing.retry_base, self._timing.unauthorized_cap)
                )
                self._give_back()
                return False
            self._failures += 1
            # Never sooner than the server's Retry-After: a summary covers a whole interval, so
            # waiting as asked costs nothing; a wait beyond the interval gives the attempt up.
            wait = max(
                full_jitter(backoff_ceiling(self._failures, self._timing.retry_base, self._timing.retry_cap)),
                sent.retry_after or 0.0,
            )
            fields: dict[str, Any] = {
                "kind": "evaluation_summary",
                "cause": sent.cause,
                "events": len(batch),
                "retry_in": wait,
            }
            if sent.status:
                fields["status"] = sent.status
            self._log(logging.WARNING, _events.EVENTS_FAILED, **fields)
            if final or now + wait >= self._give_up_at:
                self._give_back()
                return False
            self._retry_at = now + wait
            return False
        self._report = None
        return True

    def _give_back(self) -> None:
        """Puts the rows of every undelivered event back into the next window, over the same interval."""
        window = self._report
        self._report = None
        if window is not None:
            counts: dict[_Row, int] = {}
            for event in self._pending:
                for row in event["counts"]:
                    key = (row["flag"], row["variation"] or "", row["reason"])
                    counts[key] = counts.get(key, 0) + row["count"]
            self._keep(_Window(window.start, window.end, counts))
        self._pending = []

    def _delivered(self, sent: Sent, events: int) -> None:
        if sent.outcome == Outcome.DROPPED:
            self._log(
                logging.WARNING,
                _events.QUEUE_DROPPED,
                kind="evaluation_summary",
                cause="client_error",
                status=sent.status,
                events=events,
            )
        elif sent.dropped_rows or sent.rejected:
            self._log(
                logging.WARNING,
                _events.QUEUE_DROPPED,
                kind="evaluation_summary",
                cause="refused_by_the_server",
                accepted=sent.accepted,
                duplicates=sent.duplicates,
                dropped_rows=sent.dropped_rows,
                rejected=sent.rejected,
            )


class Reporter:
    """The exposure queue and the evaluation summary, as the delivery thread drives them."""

    def __init__(self, queue: EventQueue | None, summaries: Summaries | None, log: Log) -> None:
        self.queue = queue
        self.summaries = summaries
        self._log = log
        self._lock = threading.Lock()
        # Set once the flush that was asked for has run.
        self._flushes: list[threading.Event] = []

    def deadline(self) -> float | None:
        """When the reporter next has something to do, by the monotonic clock; 0 when a flush waits."""
        with self._lock:
            if self._flushes:
                return 0.0
        due = [part.deadline() for part in (self.queue, self.summaries) if part is not None]
        return min(due) if due else None

    def request_flush(self) -> threading.Event:
        done = threading.Event()
        with self._lock:
            self._flushes.append(done)
        return done

    def run(self, now: float, *, final: bool = False) -> None:
        with self._lock:
            flushes, self._flushes = self._flushes, []
        for part in (self.summaries, self.queue):
            if part is None:
                continue
            try:
                part.run(now, flush=bool(flushes), final=final)
            except Exception as err:  # reporting must never take the delivery thread down
                self._log(logging.ERROR, _events.EVALUATION_FAILED, operation="report", cause=repr(err))
        for done in flushes:
            done.set()

    def release(self) -> None:
        """Lets every flush still waiting go: the thread that would run it has ended."""
        with self._lock:
            flushes, self._flushes = self._flushes, []
        for done in flushes:
            done.set()
