"""A client survives the fork of a pre-forking server: the child restarts the thread by itself."""

from __future__ import annotations

import json
import os
import threading
import time
import warnings
from typing import Any

import pytest

import flagnetic
from mock_server import MockServer, Request, Response
from protocol import SDK_KEY, read, read_json

CONTEXT = flagnetic.Context(**read_json("scenarios.json")["context"])
RULESET = read("server/ruleset.json")
AFTER_KILL_SWITCH = read("server/ruleset-after-kill-switch.json")
ACCEPTED = read("server/events-accepted.json")


@pytest.mark.skipif(not hasattr(os, "fork"), reason="the platform has no fork")
def test_a_forked_child_serves_a_fresh_ruleset_and_sends_its_own_events() -> None:
    lock = threading.Lock()
    served: dict[str, Any] = {"document": RULESET, "etag": '"1"'}
    aliased: list[str] = []

    def handler(request: Request) -> Response:
        if request.path.endswith("/events"):
            with lock:
                aliased.extend(e["from_unit_id"] for e in json.loads(request.body)["events"])
            return Response(202, {"Content-Type": "application/json"}, ACCEPTED)
        with lock:
            document, etag = served["document"], served["etag"]
        if request.headers.get("if-none-match") == etag:
            return Response(304, {"ETag": etag})
        return Response(200, {"Content-Type": "application/json", "ETag": etag}, document)

    server = MockServer(handler)
    client = flagnetic.Client(SDK_KEY, server.url, stream=False, send_summaries=False, flush_interval=3600)
    try:
        assert client.ready() == flagnetic.Stage.FRESH
        assert client.get_bool(CONTEXT, "checkout", False) is True
        # Queued in the parent and never flushed before the fork: the parent's to send, not the child's.
        client.alias("anonymous", "parent", "user", "u-1001")
        with lock:
            served.update(document=AFTER_KILL_SWITCH, etag='"2"')

        with warnings.catch_warnings():
            # Python warns that forking a process with threads may deadlock; restarting the thread in
            # the child, with every lock made anew, is what keeps it from doing so.
            warnings.simplefilter("ignore", DeprecationWarning)
            pid = os.fork()
        if pid == 0:
            code = 1
            try:
                deadline = time.monotonic() + 10
                while client.get_bool(CONTEXT, "checkout", True) and time.monotonic() < deadline:
                    time.sleep(0.01)
                fresh = not client.get_bool(CONTEXT, "checkout", True)
                client.alias("anonymous", "child", "user", "u-1001")
                client.flush(timeout=5)
                code = 0 if fresh else 2
            finally:
                os._exit(code)
        _, status = os.waitpid(pid, 0)
        assert os.waitstatus_to_exitcode(status) == 0, "the child never served the ruleset it fetched"
        with lock:
            assert aliased == ["child"]

        # The parent's thread and queue were never touched by the child.
        assert client.get_bool(CONTEXT, "checkout", False) is True
        client.flush(timeout=5)
        with lock:
            assert aliased == ["child", "parent"]
    finally:
        client.close()
        server.close()
