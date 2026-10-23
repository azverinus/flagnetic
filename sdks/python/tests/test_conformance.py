"""Every shared fixture, with no file or case skipped."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import pytest

from flagnetic._eval import Context, decode, evaluate
from flagnetic._eval._re2 import RegexError, compile_pattern
from flagnetic._eval.bucket import bucket, murmur3

from conformance import FIXTURES, ROOT, load, same_json

MURMUR = FIXTURES / "murmur3.json"
REGEX = FIXTURES / "regex.json"
RULESET_FIXTURES = sorted(p for p in FIXTURES.rglob("*.json") if p not in (MURMUR, REGEX))


def test_the_suite_is_found() -> None:
    # A missing directory must fail, not pass with nothing to run.
    assert len(RULESET_FIXTURES) >= 30
    assert MURMUR.is_file()
    assert REGEX.is_file()


@pytest.mark.parametrize("vector", load(MURMUR)["vectors"], ids=lambda v: repr(v["input"])[:40])
def test_murmur3(vector: dict[str, Any]) -> None:
    assert murmur3(vector["input"].encode("utf-8")) == vector["hash"]
    if "." in vector["input"]:
        salt, unit_id = vector["input"].split(".", 1)
        assert bucket(salt, unit_id) == vector["bucket"]


@pytest.mark.parametrize(
    "vector", load(REGEX)["vectors"], ids=lambda v: f"{v['pattern']} ~ {v['input']!r}"[:60]
)
def test_regex(vector: dict[str, Any]) -> None:
    try:
        got: bool | str = compile_pattern(vector["pattern"])(vector["input"])
    except RegexError:
        got = "invalid"
    assert got == vector["match"]


@pytest.mark.parametrize(
    "path", RULESET_FIXTURES, ids=lambda p: p.relative_to(FIXTURES).with_suffix("").as_posix()
)
def test_fixture(path: Path) -> None:
    fixture = load(path)
    assert fixture["name"] == path.relative_to(FIXTURES).with_suffix("").as_posix()
    ruleset = decode(fixture["ruleset"])

    report = {key: [dict(c._asdict()) for c in found] for key, found in ruleset.unsupported_rules().items()}
    assert report == fixture.get("unsupported", {})

    assert fixture["cases"], "a fixture without cases tests nothing"
    for case in fixture["cases"]:
        raw = case["context"]
        context = Context(units=raw.get("units") or {}, attributes=raw.get("attributes") or {})
        got = evaluate(ruleset, context, case["flag"]).as_dict()
        assert same_json(got, case["expect"]), f"{case['name']}: got {got}, want {case['expect']}"


def test_every_reason_has_an_openfeature_mapping() -> None:
    mapping = load(ROOT / "openfeature" / "reasons.json")
    evaluator_reasons = {
        "off",
        "override",
        "rule",
        "rollout",
        "experiment",
        "fallthrough",
        "prerequisite_failed",
        "error",
    }
    assert evaluator_reasons <= mapping["reasons"].keys()
    results = set()
    for path in RULESET_FIXTURES:
        fixture = load(path)
        ruleset = decode(fixture["ruleset"])
        for case in fixture["cases"]:
            raw = case["context"]
            context = Context(units=raw.get("units") or {}, attributes=raw.get("attributes") or {})
            results.add(evaluate(ruleset, context, case["flag"]).reason)
    assert results <= mapping["reasons"].keys()
    assert mapping["metadata_key"] == "flagnetic_reason"
