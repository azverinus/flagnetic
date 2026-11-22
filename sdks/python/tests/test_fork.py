"""A client survives the fork of a pre-forking server: the child restarts the thread by itself."""

from __future__ import annotations

import http.client
import json
import os
import signal
import socket
import threading
import time
import warnings
from typing import Any

import pytest

import flagnetic
from flagnetic import _ingest
from flagnetic._deprecation import DeprecationNotes
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


@pytest.mark.skipif(not hasattr(os, "fork"), reason="the platform has no fork")
def test_a_forked_child_lets_go_of_a_request_whose_lock_a_parent_thread_held() -> None:
    ingest = _ingest.Ingest(
        notes=DeprecationNotes(lambda *_a, **_k: None),
        base_url="http://127.0.0.1:1",
        sdk_key=SDK_KEY,
        agent="test",
        platform="test",
        version="0",
        schema_version=1,
        request_timeout=1,
    )
    parent_end, child_end = socket.socketpair()
    conn = http.client.HTTPConnection("127.0.0.1", 1)
    conn.sock = child_end
    ingest._conn = conn
    held, release = threading.Event(), threading.Event()

    def hold() -> None:
        # A parent thread in the middle of a request: it holds the lock when the process forks.
        with ingest._lock:
            held.set()
            release.wait(10)

    holder = threading.Thread(target=hold)
    holder.start()
    try:
        assert held.wait(5)
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", DeprecationWarning)
            pid = os.fork()
        if pid == 0:
            code = 1
            try:
                ingest.abandon()
                code = 0 if child_end.fileno() == -1 and ingest._conn is None else 2
            finally:
                os._exit(code)
        deadline = time.monotonic() + 5
        done, status = os.waitpid(pid, os.WNOHANG)
        while done == 0 and time.monotonic() < deadline:
            time.sleep(0.01)
            done, status = os.waitpid(pid, os.WNOHANG)
        if done == 0:
            os.kill(pid, signal.SIGKILL)
            os.waitpid(pid, 0)
            pytest.fail("the child blocked on the lock the parent's thread held at the fork")
        assert os.waitstatus_to_exitcode(status) == 0
        # The parent shares the socket and keeps it.
        assert child_end.fileno() != -1
    finally:
        release.set()
        holder.join(5)
        parent_end.close()
        child_end.close()
