"""A received Deprecation header is logged once per operation and never fails the call."""

from __future__ import annotations

import logging
import time
from typing import Any

import pytest

import flagnetic
from flagnetic import events
from mock_server import MockServer, Request, Response
from protocol import SDK_KEY, Logs, eventually, read

RULESET = read("server/ruleset.json")
ACCEPTED = read("server/events-accepted.json")
DEPRECATION = "@1893456000"
SUNSET = "Wed, 01 Jan 2031 00:00:00 GMT"
JSON = {"Content-Type": "application/json"}


class Server:
    """Serves the three operations of the SDK API, each marked deprecated unless ``marked`` leaves it out."""

    def __init__(self, marked: set[str], *, sunset: bool = True) -> None:
        self.marked = marked
        self.sunset = sunset
        self.server = MockServer(self._handle)

    def _headers(self, operation: str) -> dict[str, str]:
        if operation not in self.marked:
            return {}
        out = {"Deprecation": DEPRECATION}
        if self.sunset:
            out["Sunset"] = SUNSET
        return out

    def _handle(self, request: Request) -> Response:
        if request.path.endswith("/events"):
            return Response(202, {**JSON, **self._headers("events")}, ACCEPTED)
        if request.path.endswith("/stream"):
            return Response(
                200, {"Content-Type": "text/event-stream", **self._headers("stream")}, stream=True
            )
        return Response(200, {**JSON, "ETag": '"1"', **self._headers("ruleset")}, RULESET)


@pytest.fixture
def run() -> Any:
    opened: list[tuple[Server, flagnetic.Client]] = []

    def make(marked: set[str], *, sunset: bool = True, **options: Any) -> tuple[Logs, flagnetic.Client]:
        logs = Logs()
        server = Server(marked, sunset=sunset)
        options.setdefault("stream", False)
        options.setdefault("flush_interval", 3600)
        options.setdefault("summary_interval", 3600)
        client = flagnetic.Client(SDK_KEY, server.server.url, logger=logs, **options)
        opened.append((server, client))
        assert client.ready() == flagnetic.Stage.FRESH
        return logs, client

    yield make
    for server, client in opened:
        client.close(timeout=1)
        server.server.close()


def test_the_name_is_shared() -> None:
    assert events.DEPRECATED_OPERATION == "deprecated_operation"


def test_a_deprecated_ruleset_operation_warns_once_with_its_sunset(run: Any) -> None:
    logs, client = run({"ruleset"}, poll_interval=0.02)
    time.sleep(0.3)  # several fetches
    assert logs.count(events.RULESET_LOADED) >= 1
    assert list(logs.named(events.DEPRECATED_OPERATION)) == [
        {"operation": "GET /sdk/v1/ruleset", "deprecation": DEPRECATION, "sunset": SUNSET}
    ]
    levels = {level for level, name, _ in logs.events if name == events.DEPRECATED_OPERATION}
    assert levels == {logging.WARNING}
    assert client.get_bool(flagnetic.Context(units={"user": "u"}), "checkout", False) in (True, False)


def test_a_deprecation_without_a_sunset_names_none(run: Any) -> None:
    logs, _client = run({"ruleset"}, sunset=False)
    assert logs.named(events.DEPRECATED_OPERATION) == [
        {"operation": "GET /sdk/v1/ruleset", "deprecation": DEPRECATION}
    ]


def test_an_operation_that_is_not_deprecated_logs_nothing(run: Any) -> None:
    logs, _client = run(set())
    assert logs.count(events.DEPRECATED_OPERATION) == 0


def test_events_and_the_stream_are_told_apart_and_each_warns_once(run: Any) -> None:
    logs, client = run({"events", "stream"}, stream=True)
    client.alias("anonymous", "d-1", "user", "u-1")
    client.flush()
    client.alias("anonymous", "d-2", "user", "u-2")
    client.flush()

    def seen() -> list[str]:
        ops = sorted(f["operation"] for f in logs.named(events.DEPRECATED_OPERATION))
        return [] if ops == ["GET /sdk/v1/stream", "POST /sdk/v1/events"] else [str(ops)]

    assert eventually(seen) == []
    # The ruleset is not marked: its operation never appears.
    assert logs.count(events.DEPRECATED_OPERATION) == 2


def test_a_malformed_header_never_fails_the_call() -> None:
    logs = Logs()
    mock = MockServer(
        lambda _request: Response(200, {**JSON, "ETag": '"1"', "Deprecation": "\x7f?", "Sunset": ""}, RULESET)
    )
    client = flagnetic.Client(SDK_KEY, mock.url, stream=False, logger=logs)
    try:
        assert client.ready() == flagnetic.Stage.FRESH
        assert logs.count(events.RULESET_LOADED) == 1
    finally:
        client.close(timeout=1)
        mock.close()
