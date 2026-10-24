"""Plays the shared protocol scenarios against a mock server, through the public API only."""

from __future__ import annotations

import json
import tempfile
import threading
import time
from collections.abc import Callable, Mapping
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

import flagnetic
from mock_server import MockServer, Request, Response

from conformance import ROOT, same_json

PROTOCOL = ROOT / "protocol"

SDK_KEY = "fn_srv_protocol_test_key"

# A step that waits is given this much slack, in seconds, for a thread to be scheduled late.
LATE = 0.3
EARLY = 0.02
# How long an expectation may take to come true after its action is done.
SETTLE = 4.0
# The least a no_request step watches, in seconds: shorter than that proves nothing.
MIN_QUIET = 0.2


def read(name: str) -> bytes:
    return (PROTOCOL / name).read_bytes()


def read_json(name: str) -> Any:
    return json.loads(read(name))


@dataclass
class Logs:
    """The log events a client's logger hook received, by name."""

    events: list[tuple[int, str, Mapping[str, Any]]] = field(default_factory=list)
    _lock: threading.Lock = field(default_factory=threading.Lock)

    def __call__(self, level: int, event: str, fields: Mapping[str, Any]) -> None:
        with self._lock:
            self.events.append((level, event, dict(fields)))

    def count(self, event: str) -> int:
        with self._lock:
            return sum(1 for _, name, _ in self.events if name == event)

    def named(self, event: str) -> list[Mapping[str, Any]]:
        with self._lock:
            return [fields for _, name, fields in self.events if name == event]


def default_of(answer: Mapping[str, Any]) -> Any:
    """The built-in default an answer's getter is called with, as the type of its flag asks for it."""
    default = answer["default"]
    return float(default) if answer["type"] == "number" else default


def answer_mismatch(client: flagnetic.Client, context: flagnetic.Context, answer: Mapping[str, Any]) -> str:
    """How what the getter serves differs from the answer, or '' when it does not."""
    detail = client.get_detail(context, answer["flag"], default_of(answer))
    if detail.variation != answer.get("variation", "") or detail.reason != answer["reason"]:
        want = f"{answer.get('variation', '')!r}/{answer['reason']}"
        return f"served {detail.variation!r}/{detail.reason}, want {want}"
    if not same_json(detail.value, answer["value"]):
        return f"value = {detail.value!r}, want {answer['value']!r}"
    got = None if detail.experiment is None else asdict(detail.experiment)
    if not same_json(got, answer.get("experiment")):
        return f"experiment = {got!r}, want {answer.get('experiment')!r}"
    return ""


def answers_mismatch(
    client: flagnetic.Client, context: flagnetic.Context, answers: list[Mapping[str, Any]]
) -> list[str]:
    return [f"{a['flag']}: {m}" for a in answers if (m := answer_mismatch(client, context, a))]


def eventually(check: Callable[[], list[str]], timeout: float = SETTLE) -> list[str]:
    """What ``check`` complains about once it has stopped complaining or ``timeout`` has passed."""
    deadline = time.monotonic() + timeout
    while True:
        problems = check()
        if not problems or time.monotonic() > deadline:
            return problems
        time.sleep(0.005)


def static(status: int, headers: Mapping[str, str], body: bytes) -> Callable[[Request], Response]:
    return lambda _request: Response(status, headers, body)


class Script:
    """The steps of one scenario, played in order against a mock server.

    A request that arrives for a step the script has not reached yet waits for
    it; the time it arrived at is what the step's delay window is checked against.
    """

    def __init__(self, steps: list[dict[str, Any]], divisor: float, errors: list[str]) -> None:
        self.steps = steps
        self.divisor = divisor
        self.errors = errors
        self.cond = threading.Condition()
        self.current = -1
        self.consumed: set[int] = set()
        self.handled: set[int] = set()
        self.arrivals: list[Request] = []
        self.last_end = time.monotonic()
        # Once the last request of the script is answered, time keeps passing while the
        # scenario's final expectations are checked, and a client whose last attempt failed
        # retries by backoff within that time: what arrives after is not the script's.
        self.last_request = max((i for i, s in enumerate(steps) if s.get("request")), default=-1)
        self.finished = False

    def handler(self, request: Request) -> Response:
        with self.cond:
            self.arrivals.append(request)
            if self.finished:
                return Response(418)
            index = self._wait_for_step(request)
            if index is None:
                self.errors.append(f"unexpected request {request.method} {request.path}")
                return Response(418)
            self.consumed.add(index)
            step = self.steps[index]
            self._check(index, step, request)
        response = self._response(step["respond"])
        self.done(index)
        return response

    def _wait_for_step(self, request: Request) -> int | None:
        deadline = time.monotonic() + 30
        while True:
            for index, step in enumerate(self.steps):
                req = step.get("request")
                if req and index not in self.consumed and req["path"] == request.path:
                    if self.current >= index:
                        return index
                    break
            else:
                return None
            if not self.cond.wait(deadline - time.monotonic()) and time.monotonic() >= deadline:
                return None

    def _check(self, index: int, step: dict[str, Any], request: Request) -> None:
        want = step["request"]
        label = f"step {index} {step.get('note', '')}".strip()
        if request.method != want["method"]:
            self.errors.append(f"{label}: method {request.method}, want {want['method']}")
        for name, template in want.get("headers", {}).items():
            value = template.replace("<sdk key>", SDK_KEY)
            if request.headers.get(name.lower()) != value:
                self.errors.append(f"{label}: {name} = {request.headers.get(name.lower())!r}, want {value!r}")
        for name in want.get("absent_headers", []):
            if name.lower() in request.headers:
                self.errors.append(f"{label}: {name} sent, want absent")
        window = want.get("delay_ms")
        if window is not None:
            elapsed = request.arrived - self.last_end
            low = window["min"] / 1000 / self.divisor - EARLY
            high = window["max"] / 1000 / self.divisor + LATE
            if not low <= elapsed <= high:
                self.errors.append(
                    f"{label}: arrived after {elapsed:.3f}s, want {low + EARLY:.3f}-{high - LATE:.3f}s"
                )

    @staticmethod
    def _response(respond: Mapping[str, Any]) -> Response:
        if respond.get("network_error"):
            return Response(network_error=True)
        body = read(respond["body"]) if respond.get("body") else b""
        return Response(
            respond["status"], respond.get("headers", {}), body, stream=bool(respond.get("stream"))
        )

    def reach(self, index: int) -> None:
        with self.cond:
            self.current = index
            self.cond.notify_all()

    def done(self, index: int) -> None:
        with self.cond:
            self.handled.add(index)
            self.finished = self.finished or index == self.last_request
            self.last_end = time.monotonic()
            self.cond.notify_all()

    def wait_handled(self, index: int, timeout: float) -> bool:
        with self.cond:
            return self.cond.wait_for(lambda: index in self.handled, timeout)


def play(
    scenario: Mapping[str, Any],
    context: flagnetic.Context,
    answers: Mapping[str, list[Mapping[str, Any]]],
    divisor: float,
    steps: int | None = None,
) -> list[str]:
    """Plays ``scenario`` and returns everything that differed from it."""
    return Player(scenario, context, answers, divisor, steps).run()


class Player:
    """One scenario, played step by step against a client built as the scenario says."""

    def __init__(
        self,
        scenario: Mapping[str, Any],
        context: flagnetic.Context,
        answers: Mapping[str, list[Mapping[str, Any]]],
        divisor: float,
        steps: int | None,
    ) -> None:
        self.scenario = scenario
        self.context = context
        self.answers = answers
        self.divisor = divisor
        self.errors: list[str] = []
        self.logs = Logs()
        self.steps: list[dict[str, Any]] = scenario["steps"][:steps]
        self.script = Script(self.steps, divisor, self.errors)

    def run(self) -> list[str]:
        server = MockServer(self.script.handler)
        client: flagnetic.Client | None = None
        try:
            options = self.scenario["given"]["options"]
            kwargs: dict[str, Any] = {
                "poll_interval": options["poll_interval_ms"] / 1000,
                "stream": options["sse"],
                "logger": self.logs,
                "_time_divisor": self.divisor,
            }
            stored = self.scenario["given"].get("stored")
            if stored:
                path = Path(tempfile.mkdtemp()) / "ruleset.json"
                path.write_bytes(read(stored))
                kwargs["store"] = path
            self.script.last_end = time.monotonic()
            client = flagnetic.Client(SDK_KEY, server.url, **kwargs)
            for index, step in enumerate(self.steps):
                self._play(server, client, index, step)
            with self.script.cond:
                for index, step in enumerate(self.steps):
                    if step.get("request") and index not in self.script.consumed:
                        self.errors.append(f"step {index} {step.get('note', '')}: the request never arrived")
        finally:
            if client is not None:
                client.close()
            server.close()
        return self.errors

    def _play(self, server: MockServer, client: flagnetic.Client, index: int, step: dict[str, Any]) -> None:
        script = self.script
        label = f"step {index} {step.get('note', '')}".strip()
        if step.get("request"):
            script.reach(index)
            window = step["request"].get("delay_ms") or {"max": 0}
            if not script.wait_handled(index, window["max"] / 1000 / self.divisor + 10):
                self.errors.append(f"{label}: the request never arrived")
                return
        elif "stream_send" in step:
            server.stream_send(step["stream_send"].encode())
            script.done(index)
        elif step.get("stream_close"):
            server.stream_close()
            script.done(index)
        elif "advance_ms" in step:
            time.sleep(step["advance_ms"] / 1000 / self.divisor)
            script.done(index)
        elif "no_request" in step:
            watch = step["no_request"]
            with script.cond:
                before = len(script.arrivals)
            time.sleep(max(watch["for_ms"] / 1000 / self.divisor, MIN_QUIET))
            with script.cond:
                for request in script.arrivals[before:]:
                    if watch.get("path") in (None, request.path):
                        self.errors.append(f"{label}: {request.path} was requested, want no request")
            script.done(index)
        elif set(step) - {"note", "expect"}:
            self.errors.append(f"{label}: this runner does not play {sorted(step)}")
            return
        expect = step.get("expect")
        if expect:
            problems = eventually(lambda: self._unmet(client, expect))
            self.errors.extend(f"{label}: {p}" for p in problems)

    def _unmet(self, client: flagnetic.Client, expect: Mapping[str, Any]) -> list[str]:
        problems: list[str] = []
        status = client.status()
        if "stage" in expect and status.stage.value != expect["stage"]:
            problems.append(f"stage = {status.stage.value!r}, want {expect['stage']!r}")
        if expect.get("stale_since") == "set" and status.stale_since is None:
            problems.append("stale_since unset, want set")
        if expect.get("stale_since") == "unset" and status.stale_since is not None:
            problems.append(f"stale_since = {status.stale_since}, want unset")
        if "answers_of" in expect:
            problems.extend(answers_mismatch(client, self.context, self.answers[expect["answers_of"]]))
        for event, count in expect.get("logs", {}).items():
            if self.logs.count(event) != count:
                problems.append(f"{event} logged {self.logs.count(event)} times, want {count}")
        return problems
