"""The client's own behaviour, through its public API against a mock server."""

from __future__ import annotations

import gzip
import hashlib
import http.client
import json
import logging
import os
import re
import resource
import stat
import threading
import time
from functools import partial
from pathlib import Path
from typing import Any

import pytest

import flagnetic
from flagnetic import _delivery, _ingest, _store
from mock_server import MockServer, Request, Response
from protocol import SDK_KEY, Logs, eventually, read, read_json

from conformance import ROOT as CONFORMANCE

RULESET = read("server/ruleset.json")
AFTER_KILL_SWITCH = read("server/ruleset-after-kill-switch.json")
CONTEXT = flagnetic.Context(**read_json("scenarios.json")["context"])
JSON = {"Content-Type": "application/json", "ETag": '"1"'}


def serving(document: bytes, etag: str = '"1"') -> MockServer:
    """A server that serves ``document`` on the ruleset endpoint and refuses the ruleset stream."""

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


def test_a_gzipped_ruleset_that_expands_past_the_cap_is_a_failed_fetch(
    clients: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    logs = Logs()
    monkeypatch.setattr(_delivery, "MAX_RULESET_BYTES", 1 << 20)
    bomb = gzip.compress(b" " * (8 << 20))
    assert len(bomb) < 1 << 20

    def handler(_request: Request) -> Response:
        return Response(200, {**JSON, "Content-Encoding": "gzip"}, bomb)

    client = clients(MockServer(handler), logger=logs)
    assert client.ready(5) is flagnetic.Stage.BUILT_IN
    assert [f["cause"] for f in logs.named("fetch_failed")] == ["network"]


def test_a_gzipped_ruleset_whose_wire_bytes_pass_the_cap_is_a_failed_fetch(
    clients: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    logs = Logs()
    monkeypatch.setattr(_delivery, "MAX_RULESET_BYTES", 1 << 16)
    # Random bytes do not compress: the body is larger on the wire than the cap.
    noise = gzip.compress(os.urandom(1 << 18))

    def handler(_request: Request) -> Response:
        return Response(200, {**JSON, "Content-Encoding": "gzip"}, noise)

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
    # Byte for byte what every server SDK writes, so a store moves between them.
    digest = hashlib.sha256(SDK_KEY.encode()).hexdigest().encode()
    assert store.read_bytes() == b'{"flagnetic_store":1,"key":"' + digest + b'"}\n' + RULESET
    assert SDK_KEY.encode() not in store.read_bytes()
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
    store.write_bytes(_store.encode(SDK_KEY, content))
    logs = Logs()
    client = flagnetic.Client(
        SDK_KEY, "http://127.0.0.1:1", stream=False, request_timeout=1, store=store, logger=logs
    )
    try:
        assert client.stage is flagnetic.Stage.BUILT_IN
        assert [f["op"] for f in logs.named("store_failed")] == ["load"]
    finally:
        client.close()


def test_a_store_written_under_another_key_is_discarded_and_the_right_ruleset_is_no_downgrade(
    tmp_path: Path,
) -> None:
    store = tmp_path / "flagnetic.json"
    # Another environment's ruleset, at a version higher than this one's.
    old_server = serving(with_version(RULESET, 99))
    old = flagnetic.Client("fn_srv_another_environment", old_server.url, stream=False, store=store)
    try:
        assert old.ready(5) is flagnetic.Stage.FRESH
    finally:
        old.close()
        old_server.close()

    seen: list[Request] = []

    def handler(request: Request) -> Response:
        seen.append(request)
        if request.path.endswith("/stream"):
            return Response(404)
        return Response(200, {"Content-Type": "application/json", "ETag": '"1"'}, RULESET)

    server = MockServer(handler)
    logs = Logs()
    client = flagnetic.Client(SDK_KEY, server.url, stream=False, store=store, logger=logs)
    try:
        assert client.stage is flagnetic.Stage.BUILT_IN
        assert client.ready(5) is flagnetic.Stage.FRESH
        failed = logs.named("store_failed")
        assert [(f["op"], f["cause"]) for f in failed] == [
            ("load", "the store was written under another key")
        ]
        assert client.get_detail(CONTEXT, "checkout", False).ruleset_version == 1
        assert "if-none-match" not in seen[0].headers
    finally:
        client.close()
        server.close()
    # The accepted ruleset rewrote the file under this key.
    assert store.read_bytes() == _store.encode(SDK_KEY, RULESET)


@pytest.mark.parametrize(
    "content",
    [
        RULESET,
        RULESET + b"\n",
        json.dumps(json.loads(RULESET), indent=2).encode(),
        _store.encode(SDK_KEY, RULESET).replace(b'"flagnetic_store":1', b'"flagnetic_store":2'),
        _store.encode(SDK_KEY, RULESET).replace(b'"flagnetic_store":1', b'"flagnetic_store":"1"'),
        _store.encode(SDK_KEY, RULESET).replace(b'"flagnetic_store":1', b'"flagnetic_store":true'),
    ],
    ids=[
        "bare",
        "bare with newline",
        "bare on several lines",
        "later format",
        "format as a string",
        "format true",
    ],
)
def test_a_store_written_under_no_key_or_in_another_format_is_discarded(
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


def test_a_refused_stream_key_stays_down_until_the_next_fetch_and_is_reported_once(clients: Any) -> None:
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

    client = clients(
        MockServer(handler), stream=True, logger=logs, _time_divisor=100, _down_poll_interval=3600
    )
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


def test_a_dropped_stream_brings_the_next_fetch_within_the_down_poll(clients: Any) -> None:
    fetches: list[Request] = []
    refuse_stream = threading.Event()

    def handler(request: Request) -> Response:
        if request.path.endswith("/stream"):
            if refuse_stream.is_set():
                return Response(503)
            return Response(200, {"Content-Type": "text/event-stream"}, stream=True)
        fetches.append(request)
        return Response(200, JSON, RULESET)

    server = MockServer(handler)
    # The stream is up, so the next fetch is an hour out (36 s compressed); the down poll is 0.3 s.
    client = clients(server, stream=True, poll_interval=3600, _time_divisor=100, _down_poll_interval=30)
    assert client.ready(5) is flagnetic.Stage.FRESH
    assert eventually(lambda: [] if fetches else ["no fetch"]) == []
    server.stream_send(b": hb\n\n")
    refuse_stream.set()
    before = len(fetches)
    server.stream_close()
    assert eventually(lambda: [] if len(fetches) > before else ["no safety poll"], 5) == []


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


def test_a_stream_closed_before_its_first_heartbeat_backs_off_and_a_heartbeat_restarts_it(
    clients: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    ceilings: list[float] = []

    def record(ceiling: float) -> float:
        ceilings.append(ceiling)
        return 0.0

    monkeypatch.setattr(_delivery, "full_jitter", record)
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
    preamble = b'retry: 3000\n\nevent: ruleset\nid: 1\ndata: {"version":1}\n\n'

    # A draining replica or a proxy answers 200 and ends the stream at once: each close is a
    # failure, so the wait doubles. A heartbeat then makes the stream stable, its close routine,
    # and the failures before it forgotten.
    def opened(count: int) -> list[str]:
        return [] if len(streams) == count else [f"{len(streams)} streams, want {count}"]

    for n in (1, 2, 3):
        assert eventually(partial(opened, n)) == []
        server.stream_send(preamble)
        server.stream_close()
    assert eventually(partial(opened, 4)) == []
    server.stream_send(b": hb\n\n")
    server.stream_close()
    assert eventually(partial(opened, 5)) == []
    server.stream_close()
    assert eventually(partial(opened, 6)) == []
    # The waits are 1 s doubling, then the routine 0-2 s, then 1 s again, all divided by 100.
    assert ceilings == pytest.approx([0.01, 0.02, 0.04, 0.02, 0.01])
    early = "closed_early"
    assert [f["cause"] for f in logs.named("sse_dropped")] == [early, early, early, "server_closed", early]


def test_close_ends_the_thread_while_the_stream_waits_for_its_answer(clients: Any) -> None:
    release = threading.Event()
    arrived = threading.Event()

    def handler(request: Request) -> Response:
        if request.path.endswith("/stream"):
            arrived.set()
            release.wait(10)
            return Response(500)
        return Response(200, JSON, RULESET)

    client = clients(MockServer(handler), stream=True)
    assert arrived.wait(5)
    started = time.monotonic()
    client.close()
    assert time.monotonic() - started < 2
    assert not [t for t in threading.enumerate() if t.name == "flagnetic" and t.is_alive()]
    release.set()


def test_a_long_outage_never_stops_the_thread() -> None:
    # Thousands of failed connections in a row, as hours of an unreachable server make.
    logs = Logs()
    client = flagnetic.Client(SDK_KEY, "http://127.0.0.1:1", logger=logs, _time_divisor=100_000)
    try:
        assert eventually(lambda: [] if logs.count("sse_dropped") > 1500 else ["too few"], 30) == []
        assert logs.count("evaluation_failed") == 0
        assert [t for t in threading.enumerate() if t.name == "flagnetic" and t.is_alive()]
    finally:
        client.close()


def test_an_event_too_long_to_be_a_version_drops_the_stream(clients: Any) -> None:
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
    for _ in range(80):
        server.stream_send(b"data: " + b"x" * 1024 + b"\n")
    assert eventually(lambda: [] if len(streams) == 2 else ["no reconnect"]) == []
    assert [f["cause"] for f in logs.named("sse_dropped")] == ["malformed"]


def test_a_descriptor_beyond_the_select_limit_does_not_stop_the_thread(clients: Any) -> None:
    # select.select raises ValueError for a descriptor of 1024 or more, which a busy server reaches.
    limit = resource.getrlimit(resource.RLIMIT_NOFILE)[0]
    if limit != resource.RLIM_INFINITY and limit < 1400:
        pytest.skip("the descriptor limit is too low to hold 1,100 descriptors")
    held = [os.open(os.devnull, os.O_RDONLY) for _ in range(1100)]
    try:
        logs = Logs()
        streams: list[Request] = []

        def handler(request: Request) -> Response:
            if request.path.endswith("/stream"):
                streams.append(request)
                return Response(200, {"Content-Type": "text/event-stream"}, stream=True)
            return Response(200, JSON, RULESET)

        server = MockServer(handler)
        client = clients(server, stream=True, logger=logs)
        assert client.ready(5) is flagnetic.Stage.FRESH
        assert eventually(lambda: [] if streams else ["no stream"]) == []
        server.stream_send(b": heartbeat\n\n")
        time.sleep(0.2)
        assert logs.count("evaluation_failed") == 0
        assert [t for t in threading.enumerate() if t.name == "flagnetic" and t.is_alive()]
    finally:
        for fd in held:
            os.close(fd)


def test_an_unexpected_exception_is_logged_once_per_kind_and_the_thread_goes_on(
    clients: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    original = _delivery.Delivery._fetch_once
    raised = {"count": 0}

    def sometimes_failing(self: Any) -> None:
        if raised["count"] < 3:
            raised["count"] += 1
            raise RuntimeError(f"unexpected {raised['count']}")
        original(self)

    monkeypatch.setattr(_delivery.Delivery, "_fetch_once", sometimes_failing)
    logs = Logs()
    client = clients(serving(RULESET), logger=logs, _time_divisor=1000)
    assert client.ready(5) is flagnetic.Stage.FRESH
    assert raised["count"] == 3
    assert client.get_bool(CONTEXT, "checkout", False) is True
    failures = logs.named("evaluation_failed")
    assert len(failures) == 1
    assert "RuntimeError" in failures[0]["cause"]


def test_flush_without_a_timeout_returns_when_the_thread_is_gone(
    clients: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    threads: list[threading.Thread] = []

    def gone(self: Any) -> None:
        # The thread can already be finished when the test looks for it, so it
        # records itself instead of being found through threading.enumerate().
        threads.append(threading.current_thread())
        raise SystemExit

    monkeypatch.setattr(_delivery.Delivery, "_loop", gone)
    client = clients(serving(RULESET), flush_interval=3600, summary_interval=3600)
    deadline = time.monotonic() + 5
    while not threads and time.monotonic() < deadline:
        time.sleep(0.01)
    thread = threads[0]
    thread.join(5)
    assert not thread.is_alive()
    client.get_bool(CONTEXT, "checkout", False)
    done = threading.Thread(target=client.flush, kwargs={"timeout": None}, daemon=True)
    done.start()
    done.join(5)
    assert not done.is_alive()


@pytest.mark.parametrize("suffix", ["\n", "\r\n", " ", "\t", "\x00", "\x7f", "é"])
def test_a_key_that_is_no_header_token_is_refused_without_repeating_it(suffix: str) -> None:
    key = SDK_KEY + suffix
    with pytest.raises(ValueError, match="flagnetic") as refused:
        flagnetic.Client(key, "http://127.0.0.1:1")
    assert SDK_KEY not in str(refused.value)
    assert SDK_KEY not in repr(refused.value)


def test_an_error_raised_while_building_a_request_is_named_by_its_class_only() -> None:
    conn = http.client.HTTPConnection("127.0.0.1", 1)
    secret = "fn_srv_secret\n"
    with pytest.raises(http.client.HTTPException) as failed:
        _delivery.send_request(conn, "GET", "/x", {"Authorization": f"Bearer {secret}"})
    assert secret.strip() not in str(failed.value)
    assert "ValueError" in str(failed.value)


def test_a_key_the_http_library_refuses_never_reaches_the_log(
    clients: Any, caplog: pytest.LogCaptureFixture, monkeypatch: pytest.MonkeyPatch
) -> None:
    # Construction refuses such a key; this hands one past it to show what fetch, stream and report log.
    bad_key = SDK_KEY + "\n"
    for target in (_delivery.Delivery, _ingest.Ingest):
        original = target.__init__

        def with_bad_key(self: Any, *args: Any, _original: Any = original, **kwargs: Any) -> None:
            _original(self, *args, **{**kwargs, "sdk_key": bad_key})

        monkeypatch.setattr(target, "__init__", with_bad_key)
    logs = Logs()
    with caplog.at_level(logging.DEBUG):
        client = clients(serving(RULESET), stream=True, logger=logs, _time_divisor=100)
        client.alias("anonymous", "d-1", "user", "u-1")
        client.flush(timeout=2)
        assert (
            eventually(lambda: [] if logs.count("sse_dropped") and logs.count("fetch_failed") else ["none"])
            == []
        )
    logged = repr(logs.events) + caplog.text
    assert SDK_KEY not in logged
    assert logs.count("evaluation_failed") == 0


INVALID = json.loads((CONFORMANCE / "fixtures" / "errors" / "codes.json").read_text(encoding="utf-8"))[
    "ruleset"
]


def test_a_condition_the_evaluator_cannot_compile_is_logged_once_per_flag_at_load(clients: Any) -> None:
    logs = Logs()
    document = json.dumps({**INVALID, "version": 7}).encode()
    client = clients(serving(document), logger=logs)
    client.ready(5)
    lines = logs.named("condition_invalid")
    keys = [line["flag"] for line in lines]
    assert keys == sorted(keys)
    assert {"invalid_regex", "undeclared_attribute"} <= set(keys)
    assert "fine" not in keys
    assert all(line["ruleset_version"] == 7 and line["conditions"] for line in lines)
    assert any(
        "matches" in reason
        for line in lines
        if line["flag"] == "invalid_regex"
        for reason in line["conditions"]
    )
    assert logs.count("rule_unsupported") == 0
    # Evaluating, or receiving the same version again, logs nothing more.
    for key in keys:
        client.get_detail(CONTEXT, key, "d")
    assert logs.count("condition_invalid") == len(keys)


def test_a_value_nested_too_deep_fails_its_flag_and_the_others_load(clients: Any) -> None:
    logs = Logs()
    depth = 100_000
    document = (
        '{"schema_version": 1, "version": 3, "scope": "server", "flags": ['
        '{"key": "deep", "type": "json", "on": true, "fallthrough": "v",'
        f' "variations": [{{"key": "v", "value": {"[" * depth + "]" * depth}}}]}},'
        ' {"key": "fine", "type": "string", "on": true, "fallthrough": "a",'
        ' "variations": [{"key": "a", "value": "A"}]}]}'
    ).encode()
    client = clients(serving(document), logger=logs)
    assert client.ready(5) == flagnetic.Stage.FRESH
    assert client.get_string(CONTEXT, "fine", "d") == "A"
    detail = client.get_detail(CONTEXT, "deep", ["d"])
    assert (detail.value, detail.error_code) == (["d"], "inconsistent_ruleset")
    assert [line["flag"] for line in logs.named("condition_invalid")] == ["deep"]


def test_a_new_ruleset_version_with_the_condition_is_logged_again(clients: Any) -> None:
    logs = Logs()
    served = [json.dumps({**INVALID, "version": 7}).encode()]

    def handler(request: Request) -> Response:
        if request.path.endswith("/stream"):
            return Response(404)
        return Response(200, {"Content-Type": "application/json"}, served[0])

    client = clients(MockServer(handler), logger=logs, poll_interval=30, _time_divisor=1000)
    client.ready(5)
    once = logs.count("condition_invalid")
    assert once
    served[0] = json.dumps({**INVALID, "version": 8}).encode()
    assert eventually(lambda: [] if logs.count("condition_invalid") == 2 * once else ["waiting"]) == []
    assert sorted({line["ruleset_version"] for line in logs.named("condition_invalid")}) == [7, 8]


def test_the_sdk_key_is_in_no_repr(clients: Any) -> None:
    client = clients(serving(RULESET))
    assert SDK_KEY not in repr(client)
    assert SDK_KEY not in repr(client._online)


@pytest.mark.parametrize("platform", ["linux\n", "lin\x00ux", "linüx"])
def test_a_platform_that_cannot_be_in_a_header_is_refused_at_construction(platform: str) -> None:
    with pytest.raises(ValueError, match="platform"):
        flagnetic.Client(SDK_KEY, "http://127.0.0.1:1", platform=platform, stream=False)


def test_a_stream_slow_to_send_headers_does_not_keep_the_first_fetch_waiting(clients: Any) -> None:
    release = threading.Event()

    def handler(request: Request) -> Response:
        if request.path.endswith("/stream"):
            release.wait(10)
            return Response(500)
        return Response(200, JSON, RULESET)

    try:
        client = clients(MockServer(handler), stream=True, request_timeout=30)
        started = time.monotonic()
        assert client.ready(5) is flagnetic.Stage.FRESH
        assert time.monotonic() - started < 4
    finally:
        release.set()


def test_a_stream_refused_once_reopens_after_a_fetch_succeeds(clients: Any) -> None:
    streams: list[Request] = []

    def handler(request: Request) -> Response:
        if request.path.endswith("/stream"):
            streams.append(request)
            if len(streams) == 1:
                return Response(403)
            return Response(200, {"Content-Type": "text/event-stream"}, stream=True)
        return Response(200, JSON, RULESET)

    client = clients(MockServer(handler), stream=True, _time_divisor=100, _down_poll_interval=30)
    assert client.ready(5) is flagnetic.Stage.FRESH
    assert eventually(lambda: [] if len(streams) >= 2 else ["the stream stayed stopped"], 5) == []
