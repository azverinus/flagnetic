"""The client's own behaviour, through its public API against a mock server."""

from __future__ import annotations

import gzip
import json
import logging
import os
import re
import stat
import threading
import time
from pathlib import Path
from typing import Any

import pytest

import flagnetic
from mock_server import MockServer, Request, Response
from protocol import SDK_KEY, Logs, eventually, read, read_json

RULESET = read("server/ruleset.json")
AFTER_KILL_SWITCH = read("server/ruleset-after-kill-switch.json")
CONTEXT = flagnetic.Context(**read_json("scenarios.json")["context"])
JSON = {"Content-Type": "application/json", "ETag": '"1"'}


def serving(document: bytes, etag: str = '"1"') -> MockServer:
    """A server that serves ``document`` on the ruleset endpoint and refuses the change stream."""

    def handler(request: Request) -> Response:
        if request.path.endswith("/stream"):
            return Response(404)
        if request.headers.get("if-none-match") == etag:
            return Response(304, {"ETag": etag})
        return Response(200, {"Content-Type": "application/json", "ETag": etag}, document)

    return MockServer(handler)


def with_flag(document: bytes, key: str, **changes: Any) -> bytes:
    """``document`` with the flag ``key`` changed as ``changes`` says."""
    ruleset = json.loads(document)
    for flag in ruleset["flags"]:
        if flag["key"] == key:
            flag.update(changes)
    return json.dumps(ruleset).encode()


def with_version(document: bytes, version: int) -> bytes:
    return json.dumps({**json.loads(document), "version": version}).encode()


@pytest.fixture
def clients() -> Any:
    made: list[flagnetic.Client] = []
    servers: list[MockServer] = []

    def make(server: MockServer, **options: Any) -> flagnetic.Client:
        servers.append(server)
        options.setdefault("stream", False)
        client = flagnetic.Client(SDK_KEY, server.url, **options)
        made.append(client)
        return client

    yield make
    for client in made:
        client.close()
    for server in servers:
        server.close()


def test_a_client_key_is_refused_at_construction() -> None:
    with pytest.raises(ValueError, match="client SDK key"):
        flagnetic.Client("fn_cli_abc", "http://127.0.0.1:1")


@pytest.mark.parametrize(
    "arguments",
    [
        ("", "http://127.0.0.1:1"),
        (SDK_KEY, "127.0.0.1:1"),
        (SDK_KEY, "ftp://example.com"),
    ],
)
def test_unusable_arguments_are_refused(arguments: tuple[str, str]) -> None:
    with pytest.raises(ValueError, match="flagnetic"):
        flagnetic.Client(*arguments)


@pytest.mark.parametrize("option", [{"request_timeout": 0}, {"poll_interval": -1}])
def test_unusable_options_are_refused(option: dict[str, Any]) -> None:
    with pytest.raises(ValueError, match="flagnetic"):
        flagnetic.Client(SDK_KEY, "http://127.0.0.1:1", **option)


def test_a_client_with_no_server_serves_the_defaults_and_ready_never_fails() -> None:
    # Nothing listens on port 1.
    client = flagnetic.Client(SDK_KEY, "http://127.0.0.1:1", stream=False, request_timeout=1)
    try:
        assert client.stage is flagnetic.Stage.BUILT_IN
        assert client.get_bool(CONTEXT, "checkout", False) is False
        detail = client.get_detail(CONTEXT, "checkout", False)
        assert (detail.reason, detail.error_code, detail.stage) == ("fallback", "no_ruleset", "built_in")
        assert detail.ruleset_version == 0
        assert client.ready(5) is flagnetic.Stage.BUILT_IN
        assert client.status().stale_since is not None
    finally:
        client.close()


def test_ready_waits_at_most_its_timeout_for_a_server_that_never_answers(clients: Any) -> None:
    release = threading.Event()
    server = MockServer(lambda _request: (release.wait(10), Response(500))[1])
    client = clients(server)
    started = time.monotonic()
    assert client.ready(0.2) is flagnetic.Stage.BUILT_IN
    assert time.monotonic() - started < 2
    release.set()


def test_close_ends_the_thread_even_during_a_request(clients: Any) -> None:
    release = threading.Event()
    arrived = threading.Event()

    def handler(_request: Request) -> Response:
        arrived.set()
        release.wait(10)
        return Response(500)

    client = clients(MockServer(handler))
    assert arrived.wait(5)
    started = time.monotonic()
    client.close()
    assert time.monotonic() - started < 2
    assert not [t for t in threading.enumerate() if t.name == "flagnetic" and t.is_alive()]
    client.close()  # idempotent
    release.set()


def test_requests_identify_the_sdk_and_ask_for_the_schema_it_reads() -> None:
    seen: list[Request] = []

    def handler(request: Request) -> Response:
        seen.append(request)
        if request.path.endswith("/stream"):
            return Response(200, {"Content-Type": "text/event-stream"}, stream=True)
        return Response(200, JSON, RULESET)

    server = MockServer(handler)
    client = flagnetic.Client(SDK_KEY, server.url)
    try:
        assert client.ready(5) is flagnetic.Stage.FRESH
        by_path = {r.path: r for r in seen}
        agent = re.compile(rf"flagnetic-python/{re.escape(flagnetic.__version__)} \(\w+\)")
        for path in ("/sdk/v1/ruleset", "/sdk/v1/stream"):
            assert agent.fullmatch(by_path[path].headers["user-agent"]), by_path[path].headers
            assert by_path[path].headers["authorization"] == f"Bearer {SDK_KEY}"
        assert by_path["/sdk/v1/ruleset"].headers["flagnetic-schema-version"] == "1"
        assert by_path["/sdk/v1/stream"].headers["accept"] == "text/event-stream"
    finally:
        client.close()
        server.close()


def test_a_base_url_may_carry_a_path() -> None:
    seen: list[str] = []

    def handler(request: Request) -> Response:
        seen.append(request.path)
        return Response(200, JSON, RULESET)

    server = MockServer(handler)
    client = flagnetic.Client(SDK_KEY, server.url + "/flagnetic/", stream=False)
    try:
        assert client.ready(5) is flagnetic.Stage.FRESH
        assert seen == ["/flagnetic/sdk/v1/ruleset"]
    finally:
        client.close()
        server.close()


def test_a_gzipped_ruleset_is_read(clients: Any) -> None:
    def handler(_request: Request) -> Response:
        return Response(200, {**JSON, "Content-Encoding": "gzip"}, gzip.compress(RULESET))

    client = clients(MockServer(handler))
    assert client.ready(5) is flagnetic.Stage.FRESH
    assert client.get_bool(CONTEXT, "checkout", False) is True


def test_a_truncated_gzip_body_is_a_failed_fetch(clients: Any) -> None:
    logs = Logs()

    def handler(_request: Request) -> Response:
        return Response(200, {**JSON, "Content-Encoding": "gzip"}, gzip.compress(RULESET)[:-20])

    client = clients(MockServer(handler), logger=logs)
    assert client.ready(5) is flagnetic.Stage.BUILT_IN
    assert [f["cause"] for f in logs.named("fetch_failed")] == ["network"]


def test_getters_serve_each_type_and_the_detail_says_why(clients: Any) -> None:
    client = clients(serving(RULESET))
    assert client.ready(5) is flagnetic.Stage.FRESH
    assert client.get_bool(CONTEXT, "checkout", False) is True
    assert client.get_string(CONTEXT, "banner_text", "") == "Try free"
    assert client.get_number(CONTEXT, "max_items", 0.0) == 10.0
    assert client.get_int(CONTEXT, "max_items", 0) == 10
    assert client.get_json(CONTEXT, "theme", None) == {"mode": "light"}
    detail = client.get_detail(CONTEXT, "checkout", False)
    assert (detail.value, detail.variation, detail.reason) == (True, "true", "rule")
    assert detail.rule_id
    assert (detail.ruleset_version, detail.stage, detail.error_code) == (1, flagnetic.Stage.FRESH, "")
    assert detail.received_at is not None
    assert client.get_detail(CONTEXT, "max_items", 0).value == 10
    assert client.get_detail(CONTEXT, "max_items", 0.5).value == 10.0
    assert client.get_detail(CONTEXT, "theme", {}).value == {"mode": "light"}


def test_a_getter_that_cannot_serve_returns_its_default_with_fallback(clients: Any) -> None:
    logs = Logs()
    client = clients(serving(RULESET), logger=logs)
    client.ready(5)
    assert client.get_bool(CONTEXT, "no_such_flag", True) is True
    missing = client.get_detail(CONTEXT, "no_such_flag", "x")
    assert (missing.value, missing.reason, missing.error_code) == ("x", "fallback", "flag_not_found")
    # A flag of another type than the getter's, logged once per flag.
    for _ in range(3):
        assert client.get_string(CONTEXT, "checkout", "dflt") == "dflt"
    mismatch = client.get_detail(CONTEXT, "checkout", "dflt")
    assert (mismatch.reason, mismatch.error_code) == ("fallback", "type_mismatch")
    assert [f["flag"] for f in logs.named("type_mismatch")] == ["checkout"]
    assert logs.named("type_mismatch")[0]["flag_type"] == "boolean"
    assert logs.named("type_mismatch")[0]["requested_type"] == "string"
    # Neither a bool nor a string is a number.
    assert client.get_number(CONTEXT, "checkout", 7.5) == 7.5


@pytest.mark.parametrize(
    ("served", "want"),
    [(10, 10), (2.0, 2), (-7, -7), (2.5, 99), (2**63, 99), (-(2**63), -(2**63)), (1e300, 99)],
)
def test_get_int_serves_only_whole_numbers_that_fit_64_bits(clients: Any, served: float, want: int) -> None:
    document = with_flag(
        RULESET, "max_items", variations=[{"key": "few", "value": served}, {"key": "many", "value": served}]
    )
    client = clients(serving(document))
    client.ready(5)
    assert client.get_int(CONTEXT, "max_items", 99) == want
    if want == 99:
        assert client.get_detail(CONTEXT, "max_items", 99).error_code == "type_mismatch"
        # The number getter still serves it.
        assert client.get_number(CONTEXT, "max_items", 0.0) == float(served)


def test_a_json_value_is_the_callers_to_modify(clients: Any) -> None:
    client = clients(serving(RULESET))
    client.ready(5)
    first = client.get_json(CONTEXT, "theme", None)
    first["mode"] = "mutated"
    assert client.get_json(CONTEXT, "theme", None) == {"mode": "light"}
    assert client.get_detail(CONTEXT, "theme", {}).value == {"mode": "light"}


def test_the_ruleset_is_persisted_and_serves_the_next_start_before_any_request(
    clients: Any, tmp_path: Path
) -> None:
    store = tmp_path / "flagnetic.json"
    first = clients(serving(RULESET), store=store)
    assert first.ready(5) is flagnetic.Stage.FRESH
    assert stat.S_IMODE(os.stat(store).st_mode) == 0o600
    assert json.loads(store.read_bytes()) == json.loads(RULESET)
    assert not [p for p in tmp_path.iterdir() if p != store]

    # The server is down at the next start: the store serves.
    second = flagnetic.Client(SDK_KEY, "http://127.0.0.1:1", stream=False, request_timeout=1, store=store)
    try:
        assert second.stage is flagnetic.Stage.LAST_GOOD
        assert second.get_bool(CONTEXT, "checkout", False) is True
        detail = second.get_detail(CONTEXT, "checkout", False)
        assert detail.received_at is not None
        assert abs(detail.received_at.timestamp() - store.stat().st_mtime) < 1
        assert second.ready(5) is flagnetic.Stage.LAST_GOOD
    finally:
        second.close()


@pytest.mark.parametrize("content", [b"{not json", b"", RULESET.replace(b'"server"', b'"client"')])
def test_a_store_that_cannot_be_loaded_is_reported_and_leaves_built_in(
    tmp_path: Path, content: bytes
) -> None:
    store = tmp_path / "flagnetic.json"
    store.write_bytes(content)
    logs = Logs()
    client = flagnetic.Client(
        SDK_KEY, "http://127.0.0.1:1", stream=False, request_timeout=1, store=store, logger=logs
    )
    try:
        assert client.stage is flagnetic.Stage.BUILT_IN
        assert [f["op"] for f in logs.named("store_failed")] == ["load"]
    finally:
        client.close()


def test_a_store_that_cannot_be_written_is_reported_and_the_ruleset_still_serves(
    clients: Any, tmp_path: Path
) -> None:
    logs = Logs()
    client = clients(serving(RULESET), store=tmp_path / "missing" / "flagnetic.json", logger=logs)
    assert client.ready(5) is flagnetic.Stage.FRESH
    assert client.get_bool(CONTEXT, "checkout", False) is True
    assert [f["op"] for f in logs.named("store_failed")] == ["save"]


def test_listeners_hear_the_flags_a_new_ruleset_may_have_changed(clients: Any) -> None:
    current: dict[str, Any] = {"document": RULESET, "etag": '"1"'}

    def handler(request: Request) -> Response:
        if request.path.endswith("/stream"):
            return Response(404)
        return Response(
            200, {"Content-Type": "application/json", "ETag": current["etag"]}, current["document"]
        )

    client = clients(MockServer(handler), poll_interval=30, _time_divisor=100)
    changes: list[list[str]] = []
    stages: list[flagnetic.Stage] = []
    never: list[list[str]] = []
    remove_never = client.on_change(never.append)
    remove_never()
    client.on_change(changes.append)
    client.on_stage(stages.append)
    assert client.ready(5) is flagnetic.Stage.FRESH
    first = eventually(lambda: [] if changes else ["no change heard"])
    assert first == []
    # Everything is new at the first ruleset.
    assert changes[0] == sorted(json.loads(RULESET)["flags"][i]["key"] for i in range(5))
    current.update(document=AFTER_KILL_SWITCH, etag='"2"')
    assert eventually(lambda: [] if len(changes) == 2 else ["not yet"]) == []
    assert "checkout" in changes[1]
    assert set(changes[1]) <= {f["key"] for f in json.loads(AFTER_KILL_SWITCH)["flags"]}
    assert stages == [flagnetic.Stage.FRESH]
    assert never == []


def test_a_failure_moves_fresh_to_last_good_and_a_success_back(clients: Any) -> None:
    answers = iter([Response(200, JSON, RULESET), Response(500), Response(304, {"ETag": '"1"'})])
    logs = Logs()

    def handler(_request: Request) -> Response:
        return next(answers, Response(304, {"ETag": '"1"'}))

    client = clients(MockServer(handler), poll_interval=30, _time_divisor=100, logger=logs)
    stages: list[flagnetic.Stage] = []
    client.on_stage(stages.append)
    assert (
        eventually(
            lambda: (
                []
                if stages == [flagnetic.Stage.FRESH, flagnetic.Stage.LAST_GOOD, flagnetic.Stage.FRESH]
                else [str(stages)]
            )
        )
        == []
    )
    assert [f["cause"] for f in logs.named("fetch_failed")] == ["http_status"]
    transitions = [(f["from"], f["to"]) for f in logs.named("stage_changed")]
    assert transitions == [("built_in", "fresh"), ("fresh", "last_good"), ("last_good", "fresh")]


def test_a_listener_that_raises_is_logged_and_does_not_stop_the_others(clients: Any) -> None:
    logs = Logs()
    client = clients(serving(RULESET), logger=logs)
    heard: list[list[str]] = []

    def failing(_changed: list[str]) -> None:
        raise RuntimeError("boom")

    client.on_change(failing)
    client.on_change(heard.append)
    assert client.ready(5) is flagnetic.Stage.FRESH
    assert eventually(lambda: [] if heard else ["not heard"]) == []
    assert "boom" in logs.named("listener_failed")[0]["cause"]


def test_a_logger_that_raises_does_not_take_the_client_down(clients: Any) -> None:
    def failing(_level: int, _event: str, _fields: Any) -> None:
        raise RuntimeError("boom")

    client = clients(serving(RULESET), logger=failing)
    assert client.ready(5) is flagnetic.Stage.FRESH
    assert client.get_bool(CONTEXT, "checkout", False) is True


def test_without_a_logger_events_go_to_the_flagnetic_logger(caplog: pytest.LogCaptureFixture) -> None:
    with caplog.at_level(logging.INFO, logger="flagnetic"):
        server = serving(RULESET)
        client = flagnetic.Client(SDK_KEY, server.url, stream=False)
        try:
            client.ready(5)
        finally:
            client.close()
            server.close()
    loaded = [
        r for r in caplog.records if r.name == "flagnetic" and r.getMessage().startswith("ruleset_loaded")
    ]
    assert loaded
    assert "ruleset_version=1" in loaded[0].getMessage()
    assert loaded[0].levelno == logging.INFO


def test_a_refused_stream_key_stays_down_and_is_reported_once(clients: Any) -> None:
    logs = Logs()
    stream_requests: list[Request] = []

    def handler(request: Request) -> Response:
        if request.path.endswith("/stream"):
            stream_requests.append(request)
            return Response(
                401,
                {"Content-Type": "application/problem+json"},
                read("server/problems/401-sdk_key_revoked.json"),
            )
        return Response(200, JSON, RULESET)

    client = clients(MockServer(handler), stream=True, logger=logs, _time_divisor=100)
    assert client.ready(5) is flagnetic.Stage.FRESH
    time.sleep(0.5)
    assert len(stream_requests) == 1
    assert logs.count("key_revoked") == 1
    dropped = logs.named("sse_dropped")
    assert [(f["cause"], f["reconnect"], f["status"]) for f in dropped] == [("unauthorized", False, 401)]


def test_stream_frames_split_across_writes_and_lines_ended_any_way_are_read(clients: Any) -> None:
    document: dict[str, Any] = {"current": RULESET, "etag": '"1"'}
    ruleset_requests: list[Request] = []
    stream_ready = threading.Event()

    def handler(request: Request) -> Response:
        if request.path.endswith("/stream"):
            stream_ready.set()
            return Response(200, {"Content-Type": "text/event-stream"}, stream=True)
        ruleset_requests.append(request)
        return Response(
            200, {"Content-Type": "application/json", "ETag": document["etag"]}, document["current"]
        )

    server = MockServer(handler)
    client = clients(server, stream=True, poll_interval=3600)
    assert client.ready(5) is flagnetic.Stage.FRESH
    assert stream_ready.wait(5)
    document.update(current=AFTER_KILL_SWITCH, etag='"2"')
    # CRLF line ends, the event split in the middle of a field name and of the line end.
    for piece in (b": hb\r\n\r\nev", b"ent: ruleset\r", b'\nid: 2\r\ndata: {"vers', b'ion":2}\r', b"\n\r\n"):
        server.stream_send(piece)
        time.sleep(0.02)
    assert (
        eventually(lambda: [] if client.get_bool(CONTEXT, "checkout", True) is False else ["still v1"]) == []
    )
    assert ruleset_requests[-1].headers["if-none-match"] == '"1"'
    # A bare CR also ends a line; the last one waits for the byte that shows it is not half of a CRLF.
    document.update(current=with_version(RULESET, 3), etag='"3"')
    server.stream_send(b'event: ruleset\rdata: {"version":3}\r\r')
    server.stream_send(b": hb\n\n")
    assert (
        eventually(
            lambda: [] if client.get_detail(CONTEXT, "checkout", False).ruleset_version == 3 else ["no"]
        )
        == []
    )


def test_a_malformed_ruleset_event_drops_the_stream_and_it_reconnects(clients: Any) -> None:
    logs = Logs()
    streams: list[Request] = []

    def handler(request: Request) -> Response:
        if request.path.endswith("/stream"):
            streams.append(request)
            return Response(200, {"Content-Type": "text/event-stream"}, stream=True)
        return Response(200, JSON, RULESET)

    server = MockServer(handler)
    client = clients(server, stream=True, logger=logs, _time_divisor=100, poll_interval=3600)
    assert client.ready(5) is flagnetic.Stage.FRESH
    assert eventually(lambda: [] if streams else ["no stream"]) == []
    server.stream_send(b"event: ruleset\ndata: not json\n\n")
    assert eventually(lambda: [] if len(streams) == 2 else ["no reconnect"]) == []
    assert [f["cause"] for f in logs.named("sse_dropped")] == ["malformed"]
    assert streams[1].headers["last-event-id"] == "1"
