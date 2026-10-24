"""Offline clients for tests: flagnetic.testing and the offline option, with no server at all."""

from __future__ import annotations

import json
import threading
from pathlib import Path

import pytest

import flagnetic
from flagnetic import testing
from protocol import Logs, read, read_json

CONTEXT = flagnetic.Context(**read_json("scenarios.json")["context"])
EXPERIMENT = read("server/ruleset-experiment.json")


def test_from_values_serves_each_value_by_its_type_as_fallthrough() -> None:
    client = testing.from_values(
        {"checkout": True, "banner": "Hello", "max_items": 3, "ratio": 0.5, "theme": {"mode": "dark"}}
    )
    assert client.stage == flagnetic.Stage.FRESH
    assert client.ready(timeout=0) == flagnetic.Stage.FRESH
    assert client.get_bool(CONTEXT, "checkout", False) is True
    assert client.get_string(CONTEXT, "banner", "") == "Hello"
    assert client.get_int(CONTEXT, "max_items", 0) == 3
    assert client.get_number(CONTEXT, "ratio", 0.0) == 0.5
    assert client.get_json(CONTEXT, "theme", None) == {"mode": "dark"}
    detail = client.get_detail(CONTEXT, "checkout", False)
    assert (detail.reason, detail.variation) == ("fallthrough", "value")


def test_set_rewrites_a_flag_and_fires_the_change_listeners() -> None:
    client = testing.from_values({"checkout": True})
    heard: list[list[str]] = []
    client.on_change(heard.append)
    client.set("checkout", False)
    client.set("new_flag", "on")
    assert client.get_bool(CONTEXT, "checkout", True) is False
    assert client.get_string(CONTEXT, "new_flag", "") == "on"
    assert heard == [["checkout"], ["new_flag"]]


def test_set_keeps_the_flag_type() -> None:
    client = testing.from_values({"checkout": True, "theme": {"mode": "dark"}})
    with pytest.raises(TypeError, match="boolean"):
        client.set("checkout", "yes")
    client.set("theme", ["any", "json"])
    assert client.get_json(CONTEXT, "theme", None) == ["any", "json"]


def test_reload_discards_what_set_changed() -> None:
    client = testing.from_values({"checkout": True})
    client.set("checkout", False)
    client.reload()
    assert client.get_bool(CONTEXT, "checkout", False) is True


def test_from_file_reads_the_file_again_on_reload(tmp_path: Path) -> None:
    path = tmp_path / "ruleset.json"
    path.write_bytes(read("server/ruleset.json"))
    client = testing.from_file(path)
    assert client.get_bool(CONTEXT, "checkout", False) is True
    path.write_bytes(read("server/ruleset-after-kill-switch.json"))
    client.reload()
    assert client.get_bool(CONTEXT, "checkout", True) is False


def test_a_reload_that_does_not_decode_raises_and_keeps_the_held_ruleset(tmp_path: Path) -> None:
    path = tmp_path / "ruleset.json"
    path.write_bytes(read("server/ruleset.json"))
    client = testing.from_file(path)
    path.write_text("{")
    with pytest.raises(ValueError, match="JSON"):
        client.reload()
    assert client.get_bool(CONTEXT, "checkout", False) is True


@pytest.mark.parametrize("document", ["{", "[]", json.dumps({"schema_version": 2, "version": 1})])
def test_a_document_that_does_not_decode_is_a_value_error(document: str) -> None:
    with pytest.raises(ValueError, match="flagnetic"):
        testing.from_document(document)


def test_the_recorder_holds_counts_exposures_and_aliases_with_repeats_suppressed() -> None:
    client = testing.from_document(EXPERIMENT)
    for _ in range(2):
        client.get_string(CONTEXT, "pricing", "Built-in price")
        client.exposure(CONTEXT, "pricing")
    client.get_bool(CONTEXT, "recommendations", False)
    client.get_bool(CONTEXT, "missing", False)
    client.alias("anonymous", "d-8f1c", "user", "u-1001")
    recorder = client.recorder
    assert [(e.flag_key, e.experiment_key, e.variation_key, e.unit_id) for e in recorder.exposures()] == [
        ("pricing", "pricing_test", "treatment", "u-1001"),
        ("recommendations", "recommendations_test", "on", "u-1001"),
    ]
    exposure = recorder.exposures()[0]
    assert (exposure.evaluation_reason, exposure.ruleset_version, exposure.override) == (
        "experiment",
        3,
        False,
    )
    assert (exposure.platform, exposure.app_version) == ("linux", "1.18.0")
    assert [(a.from_unit_id, a.to_unit_id) for a in recorder.aliases()] == [("d-8f1c", "u-1001")]
    assert [(c.flag, c.variation, c.reason, c.count) for c in recorder.summary()] == [
        ("missing", "", "fallback", 1),
        ("pricing", "treatment", "experiment", 2),
        ("recommendations", "on", "experiment", 1),
    ]
    recorder.reset()
    assert (recorder.summary(), recorder.exposures(), recorder.aliases()) == ([], [], [])


def test_an_exposure_reports_what_was_served_for_that_context() -> None:
    logs = Logs()
    client = testing.from_document(EXPERIMENT, logger=logs)
    other = flagnetic.Context(units={"user": "u-2002"}, attributes={})
    client.exposure(CONTEXT, "pricing")
    client.get_string(CONTEXT, "pricing", "Built-in price")
    client.exposure(other, "pricing")
    assert client.recorder.exposures() == []
    assert [f["cause"] for f in logs.named("exposure_skipped")] == ["no_experiment", "no_detail"]


def test_exposure_platform_and_app_version_come_from_the_options_where_the_context_names_none() -> None:
    client = testing.from_document(EXPERIMENT, platform="ios", app_version="2.0.0")
    bare = flagnetic.Context(units=dict(CONTEXT.units))
    client.get_string(bare, "pricing", "Built-in price")
    client.exposure(bare, "pricing")
    (exposure,) = client.recorder.exposures()
    assert (exposure.platform, exposure.app_version) == ("ios", "2.0.0")


def test_with_exposures_off_nothing_is_recorded_and_it_says_so() -> None:
    logs = Logs()
    client = testing.from_document(EXPERIMENT, send_exposures=False, logger=logs)
    client.get_bool(CONTEXT, "recommendations", False)
    client.alias("anonymous", "d-8f1c", "user", "u-1001")
    assert (client.recorder.exposures(), client.recorder.aliases()) == ([], [])
    assert logs.count("exposures_off") == 1


def test_local_overrides_are_refused_without_the_option() -> None:
    logs = Logs()
    client = testing.from_values({"checkout": True}, logger=logs)
    with pytest.raises(flagnetic.LocalOverridesNotAllowed):
        client.local_overrides.set("checkout", False)
    assert client.get_bool(CONTEXT, "checkout", False) is True
    assert logs.count("override_refused") == 1


def test_a_local_override_serves_local_override_until_removed() -> None:
    logs = Logs()
    client = testing.from_values({"checkout": True, "max_items": 3}, allow_overrides=True, logger=logs)
    assert logs.count("local_overrides_allowed") == 1
    heard: list[list[str]] = []
    client.on_change(heard.append)
    overrides = client.local_overrides
    overrides.set("checkout", False)
    overrides.set("max_items", 7)
    overrides.set("unknown", {"any": "json"})
    detail = client.get_detail(CONTEXT, "checkout", True)
    assert (detail.value, detail.reason, detail.variation, detail.experiment) == (
        False,
        "local_override",
        "",
        None,
    )
    assert client.get_int(CONTEXT, "max_items", 0) == 7
    assert client.get_number(CONTEXT, "max_items", 0.0) == 7.0
    assert client.get_json(CONTEXT, "unknown", None) == {"any": "json"}
    assert client.get_string(CONTEXT, "checkout", "default") == "default"
    assert overrides.list() == {"checkout": False, "max_items": 7, "unknown": {"any": "json"}}
    with pytest.raises(TypeError, match="boolean"):
        overrides.set("checkout", "no")
    overrides.remove("checkout")
    assert client.get_bool(CONTEXT, "checkout", False) is True
    overrides.clear()
    assert overrides.list() == {}
    assert heard == [["checkout"], ["max_items"], ["unknown"], ["checkout"], ["max_items", "unknown"]]
    assert "local_override" in {c.reason for c in client.recorder.summary()}


def test_a_protected_ruleset_refuses_and_drops_local_overrides(tmp_path: Path) -> None:
    logs = Logs()
    path = tmp_path / "ruleset.json"
    path.write_bytes(read("server/ruleset-unprotected.json"))
    client = testing.from_file(path, allow_overrides=True, logger=logs)
    client.local_overrides.set("checkout", False)
    heard: list[list[str]] = []
    client.on_change(heard.append)
    path.write_bytes(read("server/ruleset.json"))
    client.reload()
    assert client.local_overrides.list() == {}
    assert client.get_detail(CONTEXT, "checkout", False).reason == "rule"
    assert "checkout" in heard[0]
    with pytest.raises(flagnetic.LocalOverrideRefused):
        client.local_overrides.set("checkout", False)
    assert logs.count("override_refused") == 2


def test_an_offline_client_makes_no_request_and_flush_and_close_return_at_once() -> None:
    before = threading.active_count()
    client = flagnetic.Client("", "", offline=lambda: read("server/ruleset.json"))
    assert threading.active_count() == before
    assert client.get_bool(CONTEXT, "checkout", False) is True
    client.flush()
    client.close()


def test_testing_clients_are_not_collected_as_tests() -> None:
    assert getattr(testing.TestClient, "__test__", True) is False
