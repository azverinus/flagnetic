"""The shared protocol scenarios and degradation cases, played through the public API."""

from __future__ import annotations

from typing import Any

import pytest

import flagnetic
from mock_server import MockServer, Request, Response
from protocol import (
    SDK_KEY,
    Logs,
    answers_mismatch,
    eventually,
    play,
    read,
    read_json,
    static,
)

FILES = read_json("scenarios.json")
VERSIONING = read_json("versioning-scenarios.json")
EXPERIMENTS = read_json("experiment-scenarios.json")
CONTEXT = flagnetic.Context(**FILES["context"])

# Time is compressed by one factor: the client's waits and the scenario's durations are divided by it.
# The ruleset stream's scenarios watch for silences of a fraction of a minute, so they need the margins
# a smaller factor leaves; the revoked key's backoff climbs to 15 minutes and needs a larger one.
CATCH_UP = (
    "server: a fetch below the event's version is retried after 1, 2 and 4 seconds and never downgrades"
)
DIVISORS = {
    "server: a revoked key keeps the last good and retries at a capped backoff": 100,
    "server: a stream event above the held version triggers a fetch": 40,
    "server: a dropped stream reconnects with Last-Event-ID and fetches": 40,
    "server: 45 seconds of silence reconnect the stream": 40,
    CATCH_UP: 40,
}
DEFAULT_DIVISOR = 50

# Steps that wait for the server's answer to a flush need a fetch loop that is quiet meanwhile: these
# scenarios poll every half minute, which the factor below keeps apart from the steps.
DIVISORS["server: events are retried on 5xx and 429 with the same IDs and never after a rejection"] = 10
DIVISORS["server: offline mode makes no request"] = 100


def server_scenarios() -> list[dict[str, Any]]:
    every = FILES["scenarios"] + VERSIONING["scenarios"] + EXPERIMENTS["scenarios"]
    return [s for s in every if s["mode"] == "server"]


@pytest.mark.parametrize("name", [s["name"] for s in server_scenarios()])
def test_scenario(name: str) -> None:
    scenario = next(s for s in server_scenarios() if s["name"] == name)
    answers = {**FILES["answers"], **EXPERIMENTS["answers"]}
    errors = play(scenario, CONTEXT, answers, DIVISORS.get(name, DEFAULT_DIVISOR))
    assert not errors, "\n".join(errors)


DEGRADATION = read_json("degradation/cases.json")
CASES = [c for c in DEGRADATION["cases"] if c["endpoint"] == "GET /sdk/v1/ruleset"]
EVENTS_CASES = [c for c in DEGRADATION["cases"] if c["endpoint"] == "POST /sdk/v1/events"]


@pytest.mark.parametrize("case", EVENTS_CASES, ids=lambda c: c["name"])
def test_events_degradation_case(case: dict[str, Any]) -> None:
    """A batch the server answered is done, whatever it rejected and whatever the rejection is called."""
    scenario = {
        "mode": "server",
        "given": {
            "options": {
                "sse": False,
                "poll_interval_ms": 3_600_000,
                "flush_interval_ms": 3_600_000,
                "send_summaries": False,
            }
        },
        "steps": [
            {
                "request": {"method": "GET", "path": "/sdk/v1/ruleset"},
                "respond": {"status": 200, "headers": {"ETag": '"1"'}, "body": "server/ruleset.json"},
                "expect": {"stage": "fresh"},
            },
            {"call": {"op": "alias", "times": 12}},
            {"call": {"op": "flush"}},
            {"request": {"method": "POST", "path": "/sdk/v1/events"}, "respond": case["response"]},
            {"call": {"op": "flush"}},
            {"no_request": {"path": "/sdk/v1/events", "for_ms": 600_000}},
        ],
    }
    context = flagnetic.Context(**DEGRADATION["context"])
    errors = play(scenario, context, FILES["answers"], DEFAULT_DIVISOR * 20)
    assert not errors, "\n".join(errors)


@pytest.mark.parametrize("case", CASES, ids=lambda c: c["name"])
def test_degradation_case(case: dict[str, Any]) -> None:
    """A fetch answered as the case says, while a ruleset is held, ends as the case says."""
    held = static(200, {"Content-Type": "application/json", "ETag": '"3"'}, read(case["held"]))
    response = case["response"]
    body = read(response["body"]) if response.get("body") else b""
    failure = static(response["status"], response.get("headers", {}), body)
    revalidations: list[Request] = []

    def handler(request: Request) -> Response:
        if "if-none-match" not in request.headers:
            return held(request)
        revalidations.append(request)
        return failure(request)

    logs = Logs()
    server = MockServer(handler)
    client = flagnetic.Client(
        SDK_KEY, server.url, poll_interval=30, stream=False, logger=logs, _time_divisor=100
    )
    try:
        assert eventually(lambda: [] if revalidations else ["the case's fetch has not arrived"]) == []
        want = "fresh" if case["outcome"] == "accept" else "last_good"
        context = flagnetic.Context(**DEGRADATION["context"])

        def unmet() -> list[str]:
            problems = answers_mismatch(client, context, case["answers"])
            if client.stage.value != want:
                problems.append(f"stage = {client.stage.value}, want {want}")
            return problems

        assert eventually(unmet) == []
        for event, count in case.get("logs", {}).items():
            assert logs.count(event) == count, event
    finally:
        client.close()
        server.close()
