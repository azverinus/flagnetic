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
# The change stream's scenarios watch for silences of a fraction of a minute, so they need the margins
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

# The scenarios of the fetch loop and the change stream, whole, and how many steps of each are played.
# A scenario ends with a step that sends events, which this client does not do yet: its steps up to
# there are played.
DELIVERY: dict[str, int | None] = {
    "server: the fallback chain keeps the held document through every failure": None,
    "server: a stored last good document serves before the first request": None,
    "server: a revoked key keeps the last good and retries at a capped backoff": None,
    "server: a stream event above the held version triggers a fetch": None,
    "server: a dropped stream reconnects with Last-Event-ID and fetches": None,
    "server: 45 seconds of silence reconnect the stream": None,
    CATCH_UP: None,
    "server: If-None-Match revalidates and 304 keeps the document fresh": None,
    "server: a schema_version outside the supported range is a failure logged once": 4,
}

# Scenarios about what a client reports and what a test can set: events, local overrides, offline mode
# and experiments. A client that does not have them yet cannot play them; a scenario that is neither
# here nor above fails the test below instead of silently never running.
NOT_YET = {
    "server: local overrides are refused in a protected environment",
    "server: local overrides are refused without the option",
    "server: a local override serves local_override, emits no exposure and is counted",
    "server: offline mode makes no request",
    "server: events are retried on 5xx and 429 with the same IDs and never after a rejection",
    "server: the evaluation summary counts every evaluation, fallback included",
    "server: an answer carries the experiment block",
    "server: an explicit exposure is sent with its envelope",
    "server: an evaluation alone records no exposure",
    "server: automatic exposure follows the flag's setting",
    "server: repeated exposures of one assignment are sent once",
}


def server_scenarios() -> list[dict[str, Any]]:
    every = FILES["scenarios"] + VERSIONING["scenarios"] + EXPERIMENTS["scenarios"]
    return [s for s in every if s["mode"] == "server"]


def test_every_server_scenario_is_played_or_listed() -> None:
    names = [s["name"] for s in server_scenarios()]
    assert sorted(names) == sorted(set(DELIVERY) | NOT_YET)
    assert not set(DELIVERY) & NOT_YET


@pytest.mark.parametrize("name", list(DELIVERY))
def test_scenario(name: str) -> None:
    scenario = next(s for s in server_scenarios() if s["name"] == name)
    answers = {**FILES["answers"], **EXPERIMENTS["answers"]}
    errors = play(scenario, CONTEXT, answers, DIVISORS.get(name, DEFAULT_DIVISOR), DELIVERY[name])
    assert not errors, "\n".join(errors)


DEGRADATION = read_json("degradation/cases.json")
CASES = [c for c in DEGRADATION["cases"] if c["endpoint"] == "GET /sdk/v1/ruleset"]


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
