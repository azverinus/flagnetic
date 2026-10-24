"""aready, aflush and aclose under a running event loop, which keeps running while they wait."""

from __future__ import annotations

import asyncio
import json
import threading
import time

import flagnetic
from mock_server import MockServer, Request, Response
from protocol import SDK_KEY, read, read_json

CONTEXT = flagnetic.Context(**read_json("scenarios.json")["context"])
RULESET = read("server/ruleset.json")
ACCEPTED = read("server/events-accepted.json")
# How long the server takes to answer, long enough for the loop to tick many times meanwhile.
SLOW = 0.3


def test_the_async_helpers_never_block_the_event_loop() -> None:
    lock = threading.Lock()
    sent: list[str] = []

    def handler(request: Request) -> Response:
        time.sleep(SLOW)
        if request.path.endswith("/events"):
            with lock:
                sent.extend(e["type"] for e in json.loads(request.body)["events"])
            return Response(202, {"Content-Type": "application/json"}, ACCEPTED)
        return Response(200, {"Content-Type": "application/json", "ETag": '"1"'}, RULESET)

    server = MockServer(handler)

    async def main() -> None:
        client = flagnetic.Client(SDK_KEY, server.url, stream=False, flush_interval=3600)
        ticks = 0

        async def tick() -> None:
            nonlocal ticks
            while True:
                ticks += 1
                await asyncio.sleep(0.01)

        ticker = asyncio.create_task(tick())
        try:
            assert await client.aready(timeout=5) == flagnetic.Stage.FRESH
            assert ticks > 5, "the event loop stood still while aready waited"
            assert client.get_bool(CONTEXT, "checkout", False) is True

            client.alias("anonymous", "d-8f1c", "user", "u-1001")
            before = ticks
            await client.aflush(timeout=5)
            assert ticks - before > 5, "the event loop stood still while aflush waited"
            with lock:
                assert sorted(sent) == ["alias", "evaluation_summary"]

            client.alias("anonymous", "d-8f1c", "user", "u-1002")
            before = ticks
            await client.aclose(timeout=5)
            assert ticks - before > 5, "the event loop stood still while aclose waited"
            with lock:
                assert sent.count("alias") == 2
        finally:
            ticker.cancel()
            client.close()

    try:
        asyncio.run(main())
    finally:
        server.close()
