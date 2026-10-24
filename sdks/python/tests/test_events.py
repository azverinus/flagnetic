"""What a client reports and how it sends it, beyond the protocol scenarios, against a mock server."""

from __future__ import annotations

import json
import threading
import time
from collections.abc import Callable
from typing import Any

import pytest

import flagnetic
from mock_server import MockServer, Request, Response
from protocol import SDK_KEY, Logs, eventually, read, read_json

CONTEXT = flagnetic.Context(**read_json("scenarios.json")["context"])
RULESET = read("server/ruleset.json")
ACCEPTED = read("server/events-accepted.json")


class Ingest:
    """A server serving the ruleset that records every events batch and answers as ``answer`` says."""

    def __init__(self, answer: Callable[[int], Response] | None = None) -> None:
        self.lock = threading.Lock()
        self.batches: list[dict[str, Any]] = []
        self.requests: list[Request] = []
        self.answer = answer or (lambda _n: Response(202, {"Content-Type": "application/json"}, ACCEPTED))
        self.server = MockServer(self._handle)

    def _handle(self, request: Request) -> Response:
        if not request.path.endswith("/events"):
            return Response(200, {"Content-Type": "application/json", "ETag": '"1"'}, RULESET)
        with self.lock:
            self.requests.append(request)
            self.batches.append(json.loads(request.body))
            n = len(self.batches)
        return self.answer(n)

    def events(self, kind: str) -> list[dict[str, Any]]:
        with self.lock:
            return [e for b in self.batches for e in b["events"] if e["type"] == kind]


@pytest.fixture
def ingest() -> Any:
    servers: list[Ingest] = []
    clients: list[flagnetic.Client] = []

    def make(
        answer: Callable[[int], Response] | None = None, **options: Any
    ) -> tuple[Ingest, flagnetic.Client]:
        server = Ingest(answer)
        servers.append(server)
        options.setdefault("stream", False)
        options.setdefault("flush_interval", 3600)
        options.setdefault("summary_interval", 3600)
        client = flagnetic.Client(SDK_KEY, server.server.url, **options)
        clients.append(client)
        assert client.ready() == flagnetic.Stage.FRESH
        return server, client

    yield make
    for client in clients:
        client.close(timeout=1)
    for server in servers:
        server.server.close()


def test_a_batch_carries_the_envelope_and_identifies_the_sdk(ingest: Any) -> None:
    server, client = ingest(platform="testos", app_version="3.1.0")
    client.get_bool(CONTEXT, "checkout", False)
    client.alias("anonymous", "d-8f1c", "user", "u-1001")
    client.flush()
    with server.lock:
        batches, requests = list(server.batches), list(server.requests)
    assert len(batches) == 2, "the summary and the queue go in batches of their own"
    for batch, request in zip(batches, requests, strict=True):
        assert batch["schema_version"] == 1
        sdk = {"name": "flagnetic-python", "version": flagnetic.__version__, "platform": "testos"}
        assert batch["sdk"] == sdk
        assert batch["sent_at"].endswith("Z")
        assert request.headers["content-encoding"] == "gzip"
        assert request.headers["user-agent"] == f"flagnetic-python/{flagnetic.__version__} (testos)"
    (summary,) = server.events("evaluation_summary")
    assert summary["app_version"] == "3.1.0"
    assert summary["counts"] == [{"flag": "checkout", "variation": "true", "reason": "rule", "count": 1}]
    (alias,) = server.events("alias")
    assert alias["event_id"][14] == "7", "an event ID is a UUIDv7"


def test_the_queue_is_sent_on_its_interval(ingest: Any) -> None:
    server, client = ingest(flush_interval=0.1)
    client.alias("anonymous", "d-8f1c", "user", "u-1001")
    assert eventually(lambda: [] if server.events("alias") else ["no alias sent"], timeout=2) == []


def test_a_full_batch_is_sent_without_waiting_for_the_interval(ingest: Any) -> None:
    server, client = ingest(send_summaries=False)
    for i in range(500):
        client.alias("anonymous", f"d-{i}", "user", "u-1001")
    assert eventually(lambda: [] if len(server.events("alias")) == 500 else ["not sent"], timeout=2) == []


def test_close_sends_what_is_queued(ingest: Any) -> None:
    server, client = ingest(send_summaries=False)
    client.alias("anonymous", "d-8f1c", "user", "u-1001")
    client.close()
    assert len(server.events("alias")) == 1


def test_flush_waits_at_most_its_timeout(ingest: Any) -> None:
    def slow(_n: int) -> Response:
        time.sleep(1.5)
        return Response(202, {"Content-Type": "application/json"}, ACCEPTED)

    _, client = ingest(slow, send_summaries=False)
    client.alias("anonymous", "d-8f1c", "user", "u-1001")
    start = time.monotonic()
    client.flush(timeout=0.2)
    assert time.monotonic() - start < 1


def test_a_full_queue_drops_its_oldest_events(ingest: Any) -> None:
    logs = Logs()
    # The server refuses every batch for a while, so the queue only grows.
    _, client = ingest(lambda _n: Response(503), send_summaries=False, logger=logs)
    for i in range(10_001):
        client.alias("anonymous", f"d-{i}", "user", "u-1001")
    (dropped,) = [f for f in logs.named("queue_dropped") if f["cause"] == "queue_full"]
    assert (dropped["dropped"], dropped["capacity"]) == (1, 10_000)


def test_a_batch_the_server_will_never_take_is_dropped_and_not_sent_again(ingest: Any) -> None:
    logs = Logs()
    server, client = ingest(lambda _n: Response(400), send_summaries=False, logger=logs)
    client.alias("anonymous", "d-8f1c", "user", "u-1001")
    client.flush()
    client.flush()
    assert len(server.events("alias")) == 1
    assert [f["cause"] for f in logs.named("queue_dropped")] == ["client_error"]


def test_a_summary_that_failed_merges_into_the_next_window(ingest: Any) -> None:
    logs = Logs()

    def first_refused(n: int) -> Response:
        # A Retry-After beyond the interval gives the report up at once.
        if n == 1:
            return Response(503, {"Retry-After": "7200"})
        return Response(202, {"Content-Type": "application/json"}, ACCEPTED)

    server, client = ingest(first_refused, send_exposures=False, logger=logs)
    for _ in range(2):
        client.get_bool(CONTEXT, "checkout", False)
    client.flush()
    assert logs.count("events_failed") == 1
    client.get_bool(CONTEXT, "checkout", False)
    client.flush()
    first, second = server.events("evaluation_summary")
    assert first["counts"][0]["count"] == 2
    assert second["counts"] == [{"flag": "checkout", "variation": "true", "reason": "rule", "count": 3}]
    assert second["event_id"] != first["event_id"]
    assert second["interval_start"] == first["interval_start"]


def test_a_summary_is_retried_with_the_same_event_id(ingest: Any) -> None:
    server, client = ingest(
        lambda n: Response(500) if n == 1 else Response(202, {"Content-Type": "application/json"}, ACCEPTED),
        send_exposures=False,
        _time_divisor=20,
    )
    client.get_bool(CONTEXT, "checkout", False)
    client.flush(timeout=0)
    assert eventually(lambda: [] if len(server.events("evaluation_summary")) == 2 else ["no retry"]) == []
    first, second = server.events("evaluation_summary")
    assert first["event_id"] == second["event_id"]


def test_nothing_evaluated_and_nothing_queued_is_nothing_sent(ingest: Any) -> None:
    server, client = ingest()
    client.flush()
    client.close()
    assert server.batches == []


def test_close_posts_a_failing_summary_once(ingest: Any) -> None:
    # A Retry-After within the interval keeps the report waiting to go again.
    server, client = ingest(lambda _n: Response(503, {"Retry-After": "60"}), send_exposures=False)
    client.get_bool(CONTEXT, "checkout", False)
    client.flush()
    # Evaluated while the report waits: rows of a newer window.
    client.get_bool(CONTEXT, "checkout", False)
    client.close()
    first, second = server.events("evaluation_summary")
    assert first["event_id"] == second["event_id"], "close posts the report in progress, not the newer rows"


def test_close_cuts_an_events_batch_in_flight_and_sends_it_again(ingest: Any) -> None:
    released = threading.Event()

    def first_hangs(n: int) -> Response:
        if n == 1:
            released.wait(10)
        return Response(202, {"Content-Type": "application/json"}, ACCEPTED)

    server, client = ingest(first_hangs, send_summaries=False, request_timeout=30)
    try:
        client.alias("anonymous", "d-8f1c", "user", "u-1001")
        client.flush(timeout=0)
        assert eventually(lambda: [] if server.events("alias") else ["not posted"], timeout=2) == []
        start = time.monotonic()
        client.close(timeout=5)
        assert time.monotonic() - start < 2, "close waited for the request in flight"
        first, second = server.events("alias")
        assert first["event_id"] == second["event_id"]
    finally:
        released.set()


def test_flush_from_a_listener_sends_at_once() -> None:
    lock = threading.Lock()
    served: dict[str, Any] = {"document": RULESET, "etag": '"1"'}
    aliases: list[str] = []

    def handler(request: Request) -> Response:
        with lock:
            if request.path.endswith("/events"):
                aliases.extend(e["from_unit_id"] for e in json.loads(request.body)["events"])
                return Response(202, {"Content-Type": "application/json"}, ACCEPTED)
            document, etag = served["document"], served["etag"]
        return Response(200, {"Content-Type": "application/json", "ETag": etag}, document)

    server = MockServer(handler)
    client = flagnetic.Client(
        SDK_KEY, server.url, stream=False, send_summaries=False, flush_interval=3600, _time_divisor=100
    )
    took: list[float] = []

    def listener(_keys: list[str]) -> None:
        start = time.monotonic()
        client.flush(timeout=5)
        took.append(time.monotonic() - start)

    try:
        assert client.ready() == flagnetic.Stage.FRESH
        client.on_change(listener)
        client.alias("anonymous", "d-8f1c", "user", "u-1001")
        with lock:
            served.update(document=read("server/ruleset-after-kill-switch.json"), etag='"2"')
        assert eventually(lambda: [] if took else ["listener never ran"], timeout=7) == []
        assert took[0] < 2, "flush waited for the thread it runs on"
        with lock:
            assert aliases == ["d-8f1c"]
    finally:
        client.close(timeout=1)
        server.close()
