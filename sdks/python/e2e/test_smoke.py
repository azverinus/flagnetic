"""The smoke every SDK runs against two real replicas on one PostgreSQL.

`flagnetic-e2e up` brings them up and seeds them (e2e/README.md). The smoke
reads the seed from FLAGNETIC_E2E_SEED and runs the control commands through
FLAGNETIC_E2E_CMD (default: flagnetic-e2e on the PATH). Run it with
`uv run pytest e2e`; without a seed it fails rather than skips.

The tests run in the order they are written: the kill switch changes the seed
until it is restored, and the revocation ends the server key, so it comes last.
"""

from __future__ import annotations

import json
import os
import queue
import subprocess
import threading
import time
import urllib.error
import urllib.request
from collections.abc import Callable, Iterator, Mapping
from datetime import datetime
from pathlib import Path
from typing import Any

import pytest

import flagnetic

SEED_PATH = os.environ.get("FLAGNETIC_E2E_SEED", "")
if not SEED_PATH:
    raise RuntimeError("FLAGNETIC_E2E_SEED must name the seed `flagnetic-e2e up` wrote")
SEED: dict[str, Any] = json.loads(Path(SEED_PATH).read_text(encoding="utf-8"))
COMMAND = os.environ.get("FLAGNETIC_E2E_CMD", "flagnetic-e2e")


class Logs:
    """The names of the log events a client's logger hook received."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._events: list[str] = []

    def __call__(self, level: int, event: str, fields: Mapping[str, Any]) -> None:
        with self._lock:
            self._events.append(event)

    def __contains__(self, event: object) -> bool:
        with self._lock:
            return event in self._events


@pytest.fixture
def clients() -> Iterator[Callable[..., tuple[flagnetic.Client, Logs]]]:
    built: list[flagnetic.Client] = []

    def build(base_url: str, **options: Any) -> tuple[flagnetic.Client, Logs]:
        logs = Logs()
        client = flagnetic.Client(SEED["server_key"], base_url, logger=logs, **options)
        built.append(client)
        return client, logs

    yield build
    for client in built:
        client.close(timeout=2)


def control(*args: str) -> Any:
    """Runs a control command of the harness; its JSON output, or None when it printed nothing."""
    done = subprocess.run([COMMAND, *args], capture_output=True, text=True, check=False, timeout=60)
    assert done.returncode == 0, f"flagnetic-e2e {' '.join(args)}: {done.stderr}"
    return json.loads(done.stdout) if done.stdout.strip() else None


def eventually(check: Callable[[], bool], seconds: float, every: float = 0.05) -> bool:
    deadline = time.monotonic() + seconds
    while not check():
        if time.monotonic() > deadline:
            return False
        time.sleep(every)
    return True


def unit(unit_id: str) -> flagnetic.Context:
    return flagnetic.Context(units={SEED["unit_type"]: unit_id})


def unlike(case: Mapping[str, Any]) -> Any:
    """A built-in default of the case's type that the flag never serves, so a fallback cannot pass for it."""
    value = case["value"]
    match case["type"]:
        case "boolean":
            return not value
        case "number":
            return float(value) - 1
        case "string":
            return f"not {value}"
        case _:
            return {"not": value}


def report(milliseconds: int) -> None:
    """Prints the kill switch's propagation as one JSON line, and adds it to the CI job's summary."""
    line = json.dumps({"sdk": "flagnetic-python", "kill_switch_ms": milliseconds}, separators=(",", ":"))
    print(f"\n{line}")
    summary = os.environ.get("GITHUB_STEP_SUMMARY", "")
    if summary:
        with open(summary, "a", encoding="utf-8") as f:
            f.write(f"{line}\n")


def test_ready_reaches_fresh_and_every_case_serves_its_true_reason(clients: Any) -> None:
    client, _ = clients(SEED["replicas"]["a"])
    assert client.ready(timeout=10) == flagnetic.Stage.FRESH
    assert SEED["cases"]
    for case in SEED["cases"]:
        detail = client.get_detail(flagnetic.Context(**case["context"]), case["flag"], unlike(case))
        served = (detail.value, detail.variation, detail.reason)
        assert served == (case["value"], case["variation"], case["reason"]), case["name"]


# The segment is private: only a server key learns its members, and the SDK
# decides membership itself from the ruleset it holds.
def test_the_private_segment_decides_the_value(clients: Any) -> None:
    client, _ = clients(SEED["replicas"]["b"])
    assert client.ready(timeout=10) == flagnetic.Stage.FRESH
    flag = SEED["flags"]["private_segment"]
    member = client.get_detail(unit(SEED["units"]["segment_member"]), flag, False)
    assert (member.value, member.reason) == (True, "rule")
    other = client.get_detail(unit(SEED["units"]["outside_rollout"]), flag, True)
    assert (other.value, other.reason) == (False, "fallthrough")


def test_a_kill_switch_through_replica_a_reaches_the_sdk_streaming_from_replica_b_within_two_seconds(
    clients: Any,
) -> None:
    flag = SEED["flags"]["on"]
    context = unit(SEED["units"]["outside_rollout"])
    client, logs = clients(SEED["replicas"]["b"], poll_interval=3600)
    assert client.ready(timeout=10) == flagnetic.Stage.FRESH
    assert eventually(lambda: "sse_connected" in logs, 10), "the stream to replica B never connected"
    # The replica announces the version the client holds as the stream opens;
    # give it a moment so that the kill switch is news to it.
    time.sleep(0.5)
    assert client.get_bool(context, flag, False) is True

    off: queue.Queue[float] = queue.Queue()

    def changed(keys: list[str]) -> None:
        if flag in keys and client.get_detail(context, flag, True).reason == "off":
            off.put(time.time())

    client.on_change(changed)
    try:
        killed = control("kill", flag)
        assert killed["changed"], "the kill switch changed nothing"
        try:
            at = off.get(timeout=10)
        except queue.Empty:
            pytest.fail("the kill switch never reached the SDK")
        took = round((at - datetime.fromisoformat(killed["committed_at"]).timestamp()) * 1000)
        report(took)
        assert took <= 2000, f"the kill switch took {took} ms to reach the SDK"
        detail = client.get_detail(context, flag, True)
        assert (detail.value, detail.reason) == (False, "off")
    finally:
        control("restore")


# Each replica writes the SDKs it saw once a minute.
def test_the_sdk_is_in_the_inventory_under_its_own_name(clients: Any) -> None:
    client, _ = clients(SEED["replicas"]["b"])
    client.ready(timeout=10)

    def listed() -> bool:
        return any(
            entry["sdk_name"] == "flagnetic-python"
            and entry["sdk_version"] == flagnetic.__version__
            and entry["sdk_key"]["kind"] == "server"
            for entry in control("inventory")
        )

    assert eventually(listed, 90, every=2), (
        f"no flagnetic-python {flagnetic.__version__} in the inventory within 90 s"
    )


def ruleset_status(base_url: str) -> int:
    request = urllib.request.Request(
        f"{base_url}/sdk/v1/ruleset", headers={"Authorization": f"Bearer {SEED['server_key']}"}
    )
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            return int(response.status)
    except urllib.error.HTTPError as err:
        return err.code


# A replica checks the key on every request; the revocation goes through
# replica A and is refused by replica B.
def test_a_revoked_key_is_refused(clients: Any) -> None:
    control("revoke", "server")
    assert eventually(lambda: ruleset_status(SEED["replicas"]["b"]) == 401, 5, every=0.1), (
        "replica B still serves the revoked key"
    )
    client, logs = clients(SEED["replicas"]["b"])
    assert client.ready(timeout=5) == flagnetic.Stage.BUILT_IN
    assert "key_revoked" in logs, "the refusal was not logged as key_revoked"
