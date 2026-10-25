"""The OpenFeature provider, through the real OpenFeature SDK wherever it shows the behaviour."""

from __future__ import annotations

import asyncio
import json
import threading
import time
from collections.abc import Callable, Iterator, Mapping
from pathlib import Path
from typing import Any

import pytest

pytest.importorskip("openfeature")

from openfeature import api
from openfeature.client import OpenFeatureClient
from openfeature.evaluation_context import EvaluationContext
from openfeature.event import EventDetails, ProviderEvent
from openfeature.exception import ErrorCode
from openfeature.flag_evaluation import Reason
from openfeature.provider import ProviderStatus
from openfeature.track import TrackingEventDetails

import flagnetic
from flagnetic.openfeature import REASON_METADATA_KEY, Provider, reason_of
from mock_server import MockServer, Request, Response
from protocol import SDK_KEY, Logs

from conformance import FIXTURES, ROOT, load, same_json

MAPPING = load(ROOT / "openfeature" / "reasons.json")
MURMUR = FIXTURES / "murmur3.json"
REGEX = FIXTURES / "regex.json"
RULESET_FIXTURES = sorted(p for p in FIXTURES.rglob("*.json") if p not in (MURMUR, REGEX))


def _quiet(_level: int, _event: str, _fields: Mapping[str, Any]) -> None:
    pass


def _flag(key: str, kind: str, variations: list[tuple[str, Any]], fallthrough: str, **extra: Any) -> Any:
    return {
        "key": key,
        "type": kind,
        "automatic_exposure": False,
        "on": True,
        "variations": [{"key": k, "value": v} for k, v in variations],
        "off_variation": variations[0][0],
        "fallthrough": fallthrough,
        **extra,
    }


def _experiment(key: str, salt: str) -> Any:
    return {
        "id": key,
        "conditions": [],
        "serve": {
            "kind": "experiment",
            "experiment": key,
            "unit_type": "user",
            "inclusion_salt": f"in-{salt}",
            "variation_salt": f"var-{salt}",
            "traffic_allocation": 10000,
            "weights": [{"variation": "control", "weight": 0}, {"variation": "treatment", "weight": 10000}],
        },
    }


def _document(version: int = 3, greeting: str = "hi") -> dict[str, Any]:
    """A server ruleset exercising every mapping the provider owns."""
    pricing = [("control", "old"), ("treatment", "new")]
    return {
        "schema_version": 1,
        "version": version,
        "scope": "server",
        "environment": {"key": "staging", "protected": False},
        "unit_types": ["user", "account"],
        "attributes": [{"key": "os", "type": "string"}],
        "segments": [],
        "flags": [
            _flag(
                "new_checkout",
                "boolean",
                [("off", False), ("on", True)],
                "off",
                rules=[
                    {
                        "id": "r1",
                        "conditions": [
                            {"kind": "attribute", "attribute": "os", "op": "eq", "value": "linux"}
                        ],
                        "serve": {"kind": "variation", "variation": "on"},
                    }
                ],
            ),
            {**_flag("disabled_flag", "boolean", [("off", False), ("on", True)], "on"), "on": False},
            _flag("greeting", "string", [("hello", "hello"), ("hi", "hi")], greeting),
            _flag("page_size", "number", [("small", 10), ("large", 50)], "small"),
            _flag("ratio", "number", [("half", 0.5)], "half"),
            _flag("theme", "json", [("dark", {"background": "black"})], "dark"),
            _flag("pricing", "string", pricing, "control", rules=[_experiment("pricing_test", "p")]),
            {
                **_flag("auto_flag", "string", pricing, "control", rules=[_experiment("auto_test", "a")]),
                "automatic_exposure": True,
            },
        ],
    }


@pytest.fixture(autouse=True)
def _reset_openfeature() -> Iterator[None]:
    yield
    api.shutdown()


def register(provider: Provider, domain: str) -> OpenFeatureClient:
    """Sets ``provider`` for ``domain`` once it has initialized."""
    # set_provider initializes in place before 0.10, which added set_provider_and_wait and made
    # set_provider initialize in the background; the extra accepts both.
    getattr(api, "set_provider_and_wait", api.set_provider)(provider, domain)
    return api.get_client(domain)


@pytest.fixture
def offline(request: pytest.FixtureRequest) -> Callable[..., tuple[Provider, OpenFeatureClient]]:
    """Registers an offline provider over ``_document()`` under a domain of the test's own."""

    def build(**options: Any) -> tuple[Provider, OpenFeatureClient]:
        provider = Provider("", "", offline=_document(), logger=_quiet, **options)
        return provider, register(provider, request.node.name)

    return build


USER = EvaluationContext("u-1")


def wait_until(condition: Callable[[], bool], timeout: float = 5.0) -> None:
    deadline = time.monotonic() + timeout
    while not condition():
        assert time.monotonic() < deadline, "timed out"
        time.sleep(0.005)


# -- evaluation -----------------------------------------------------------


def test_metadata_names_the_provider() -> None:
    assert Provider("", "", offline=_document()).get_metadata().name == "flagnetic"


def test_ready_through_the_openfeature_sdk(offline: Any) -> None:
    provider, client = offline()
    assert client.get_provider_status() is ProviderStatus.READY
    assert provider.client.stage is flagnetic.Stage.FRESH


def test_a_rule_is_a_targeting_match(offline: Any) -> None:
    _, client = offline()
    got = client.get_boolean_details("new_checkout", False, EvaluationContext("u-1", {"os": "linux"}))
    assert (got.value, got.reason, got.variant) == (True, Reason.TARGETING_MATCH, "on")
    assert got.flag_metadata["flagnetic_reason"] == "rule"
    assert got.flag_metadata["stage"] == "fresh"
    assert got.flag_metadata["ruleset_version"] == 3
    assert got.flag_metadata["variation"] == "on"
    assert got.flag_metadata["overridden"] is False
    assert got.error_code is None


def test_no_match_falls_through_to_default(offline: Any) -> None:
    _, client = offline()
    got = client.get_boolean_details("new_checkout", True, EvaluationContext("u-1", {"os": "mac"}))
    assert (got.value, got.reason, got.flag_metadata["flagnetic_reason"]) == (
        False,
        Reason.DEFAULT,
        "fallthrough",
    )


def test_an_off_flag_is_disabled(offline: Any) -> None:
    _, client = offline()
    got = client.get_boolean_details("disabled_flag", True, USER)
    assert (got.value, got.reason) == (False, Reason.DISABLED)


def test_string_and_float(offline: Any) -> None:
    _, client = offline()
    assert client.get_string_value("greeting", "def", USER) == "hi"
    ratio = client.get_float_details("ratio", 0.0, USER)
    assert (ratio.value, ratio.reason) == (0.5, Reason.DEFAULT)
    whole = client.get_float_details("page_size", 0.0, USER)
    assert whole.value == 10.0 and isinstance(whole.value, float)


def test_integer_reads_a_whole_number_and_refuses_a_fraction(offline: Any) -> None:
    _, client = offline()
    size = client.get_integer_details("page_size", 0, USER)
    assert size.value == 10 and type(size.value) is int and size.reason == Reason.DEFAULT
    ratio = client.get_integer_details("ratio", 7, USER)
    assert (ratio.value, ratio.reason, ratio.error_code) == (7, Reason.ERROR, ErrorCode.TYPE_MISMATCH)


def test_object_is_served_as_a_copy(offline: Any) -> None:
    _, client = offline()
    got = client.get_object_details("theme", {}, USER)
    assert got.value == {"background": "black"}
    assert got.reason == Reason.DEFAULT
    got.value["background"] = "white"
    assert client.get_object_value("theme", {}, USER) == {"background": "black"}


def test_object_fallback_returns_the_default_itself(offline: Any) -> None:
    _, client = offline()
    default = {"keep": ["me"]}
    got = client.get_object_details("missing", default, USER)
    assert got.value is default
    assert (got.reason, got.error_code) == (Reason.ERROR, ErrorCode.FLAG_NOT_FOUND)


def test_flag_not_found_carries_the_flagnetic_reason(offline: Any) -> None:
    _, client = offline()
    got = client.get_boolean_details("missing", True, USER)
    assert (got.value, got.reason, got.error_code, got.variant) == (
        True,
        Reason.ERROR,
        ErrorCode.FLAG_NOT_FOUND,
        None,
    )
    assert got.error_message == "flag_not_found"
    assert got.flag_metadata["flagnetic_reason"] == "fallback"


def test_type_mismatch(offline: Any) -> None:
    _, client = offline()
    got = client.get_string_details("new_checkout", "def", USER)
    assert (got.value, got.reason, got.error_code) == ("def", Reason.ERROR, ErrorCode.TYPE_MISMATCH)


def test_no_ruleset_is_a_general_error() -> None:
    server = MockServer(lambda _request: Response(500))
    provider = Provider(SDK_KEY, server.url, logger=_quiet, ready_timeout=0.05)
    try:
        got = provider.resolve_boolean_details("anything", True, USER)
        assert (got.value, got.reason, got.error_code) == (True, Reason.ERROR, ErrorCode.GENERAL)
        assert got.error_message == "no_ruleset"
        assert got.flag_metadata["stage"] == "built_in"
    finally:
        provider.shutdown()
        server.close()


def test_an_experiment_is_a_split_carrying_its_block(offline: Any) -> None:
    _, client = offline()
    got = client.get_string_details("pricing", "def", USER)
    assert (got.value, got.reason, got.variant) == ("new", Reason.SPLIT, "treatment")
    meta = got.flag_metadata
    assert meta["flagnetic_reason"] == "experiment"
    assert meta["experiment_key"] == "pricing_test"
    assert meta["experiment_variation"] == "treatment"
    assert meta["experiment_unit_type"] == "user"
    assert meta["experiment_unit_id"] == "u-1"
    assert isinstance(meta["experiment_inclusion_bucket"], int)
    assert isinstance(meta["experiment_variation_bucket"], int)
    assert meta["experiment_override"] is False


def test_the_units_attribute_adds_further_unit_types(offline: Any) -> None:
    _, client = offline(unit_type="account")
    # The targeting key names the account unit, which the experiment does not bucket on; only the units
    # attribute adds the user unit it does.
    without = client.get_string_details("pricing", "def", EvaluationContext("acct-1"))
    assert without.reason == Reason.DEFAULT
    context = EvaluationContext("acct-1", {"units": {"user": "u-1"}})
    got = client.get_string_details("pricing", "def", context)
    assert (got.value, got.flag_metadata["experiment_unit_id"]) == ("new", "u-1")


def test_a_local_override_is_static(offline: Any) -> None:
    provider, client = offline(allow_overrides=True)
    provider.client.local_overrides.set("greeting", "overridden")
    got = client.get_string_details("greeting", "def", USER)
    assert (got.value, got.reason) == ("overridden", Reason.STATIC)
    # A local override is not a platform override.
    assert got.flag_metadata["overridden"] is False


def test_the_async_resolvers_serve_the_same(offline: Any) -> None:
    _, client = offline()
    got = asyncio.run(client.get_string_details_async("pricing", "def", USER))
    assert (got.value, got.reason) == ("new", Reason.SPLIT)


# -- the shared mapping ---------------------------------------------------


def test_every_reason_maps_as_the_shared_fixture_says() -> None:
    assert MAPPING["metadata_key"] == REASON_METADATA_KEY
    for flagnetic_reason, openfeature_reason in MAPPING["reasons"].items():
        assert reason_of(flagnetic_reason) == openfeature_reason, flagnetic_reason
    assert reason_of("a_reason_a_newer_server_adds") == Reason.UNKNOWN


_DEFAULTS: dict[str, Any] = {
    "boolean": False,
    "string": "conformance-default",
    "number": -1.0,
    "json": {"conformance": "default"},
}


@pytest.mark.parametrize(
    "path", RULESET_FIXTURES, ids=lambda p: p.relative_to(FIXTURES).with_suffix("").as_posix()
)
def test_conformance_through_the_provider(path: Path) -> None:
    """Every conformance case served through the provider: the reason mapped as the shared fixture says."""
    fixture = load(path)
    provider = Provider("", "", offline=fixture["ruleset"], logger=_quiet)
    resolvers: dict[str, Callable[..., Any]] = {
        "boolean": provider.resolve_boolean_details,
        "string": provider.resolve_string_details,
        "number": provider.resolve_float_details,
        "json": provider.resolve_object_details,
    }
    for case in fixture["cases"]:
        expect = case["expect"]
        raw = case["context"]
        # Every unit goes through the units attribute, so no case needs a unit type of its own.
        attributes = {**(raw.get("attributes") or {}), "units": raw.get("units") or {}}
        kind = expect.get("type", "boolean")
        got = resolvers[kind](case["flag"], _DEFAULTS[kind], EvaluationContext(None, attributes))
        name = f"{path.name} {case['name']}"
        assert got.reason == MAPPING["reasons"][expect["reason"]], name
        if expect["reason"] == "error":
            want_code = (
                ErrorCode.FLAG_NOT_FOUND if expect["error_code"] == "flag_not_found" else ErrorCode.GENERAL
            )
            assert (got.error_code, got.value) == (want_code, _DEFAULTS[kind]), name
            assert got.flag_metadata["flagnetic_reason"] == "fallback", name
        else:
            assert same_json(got.value, expect["value"]), name
            assert got.variant == expect["variation"], name
            assert got.flag_metadata["flagnetic_reason"] == expect["reason"], name
            assert got.error_code is None, name


# -- track ----------------------------------------------------------------


def test_exposure_through_track(offline: Any) -> None:
    provider, client = offline()
    client.get_string_value("pricing", "def", USER)
    client.track("exposure", USER, TrackingEventDetails().add("flag", "pricing"))
    (exposure,) = provider.client.recorder.exposures()
    assert (exposure.experiment_key, exposure.variation_key, exposure.unit_id) == (
        "pricing_test",
        "treatment",
        "u-1",
    )


def test_exposure_of_a_flag_without_an_experiment_records_nothing(offline: Any) -> None:
    provider, client = offline()
    client.get_string_value("greeting", "def", USER)
    client.track("exposure", USER, TrackingEventDetails().add("flag", "greeting"))
    assert provider.client.recorder.exposures() == []


def test_evaluation_alone_never_records_an_exposure(offline: Any) -> None:
    provider, client = offline()
    for _ in range(3):
        client.get_string_value("pricing", "def", USER)
    assert provider.client.recorder.exposures() == []


def test_automatic_exposure_needs_no_track(offline: Any) -> None:
    provider, client = offline()
    client.get_string_value("auto_flag", "def", USER)
    assert [e.experiment_key for e in provider.client.recorder.exposures()] == ["auto_test"]


def test_alias_through_track(offline: Any) -> None:
    provider, client = offline()
    details = (
        TrackingEventDetails()
        .add("fromUnitType", "anonymous")
        .add("fromUnitId", "anon-1")
        .add("toUnitType", "user")
        .add("toUnitId", "u-1")
    )
    client.track("alias", EvaluationContext(), details)
    (alias,) = provider.client.recorder.aliases()
    assert (alias.from_unit_type, alias.from_unit_id, alias.to_unit_type, alias.to_unit_id) == (
        "anonymous",
        "anon-1",
        "user",
        "u-1",
    )


@pytest.mark.parametrize(
    ("name", "details"),
    [
        ("exposure", None),
        ("exposure", TrackingEventDetails()),
        ("exposure", TrackingEventDetails().add("flag", 3)),
        ("alias", TrackingEventDetails().add("fromUnitType", "anonymous").add("fromUnitId", "anon-1")),
        ("purchase", TrackingEventDetails(9.99).add("flag", "pricing")),
    ],
)
def test_track_without_what_its_mapping_needs_does_nothing(
    offline: Any, name: str, details: TrackingEventDetails | None
) -> None:
    provider, client = offline()
    client.get_string_value("pricing", "def", USER)
    client.track(name, USER, details)
    assert provider.client.recorder.exposures() == []
    assert provider.client.recorder.aliases() == []


# -- events ---------------------------------------------------------------


class Heard:
    """Collects the provider events the OpenFeature SDK hands its handlers."""

    def __init__(self, client: OpenFeatureClient | None) -> None:
        """Listens to ``client``, or to every provider through the API for None."""
        self.events: list[tuple[ProviderEvent, EventDetails]] = []
        self._lock = threading.Lock()
        add_handler = client.add_handler if client is not None else api.add_handler
        for event in ProviderEvent:
            add_handler(event, self._handler(event))

    def _handler(self, event: ProviderEvent) -> Callable[[EventDetails], None]:
        def handle(details: EventDetails) -> None:
            with self._lock:
                self.events.append((event, details))

        return handle

    def kinds(self) -> list[ProviderEvent]:
        with self._lock:
            return [event for event, _ in self.events]


def test_a_change_is_configuration_changed_with_the_changed_keys(offline: Any) -> None:
    provider, client = offline(allow_overrides=True)
    heard = Heard(client)
    provider.client.local_overrides.set("greeting", "changed")
    wait_until(lambda: ProviderEvent.PROVIDER_CONFIGURATION_CHANGED in heard.kinds())
    (details,) = [d for e, d in heard.events if e is ProviderEvent.PROVIDER_CONFIGURATION_CHANGED]
    assert details.flags_changed == ["greeting"]
    assert details.provider_name == "flagnetic"


def test_a_failed_fetch_is_stale_and_recovery_is_ready() -> None:
    failing = threading.Event()
    document = json.dumps(_document()).encode()

    def handler(request: Request) -> Response:
        if request.path.endswith("/stream"):
            return Response(404)
        if failing.is_set():
            return Response(500)
        return Response(200, {"Content-Type": "application/json", "ETag": '"1"'}, document)

    server = MockServer(handler)
    provider = Provider(SDK_KEY, server.url, stream=False, logger=_quiet, _time_divisor=100)
    try:
        client = register(provider, "stale")
        assert provider.client.stage is flagnetic.Stage.FRESH
        heard = Heard(client)
        failing.set()
        wait_until(lambda: ProviderEvent.PROVIDER_STALE in heard.kinds())
        assert client.get_provider_status() is ProviderStatus.STALE
        stale_at = heard.kinds().index(ProviderEvent.PROVIDER_STALE)
        failing.clear()
        wait_until(lambda: ProviderEvent.PROVIDER_READY in heard.kinds()[stale_at:])
        assert client.get_provider_status() is ProviderStatus.READY
    finally:
        server.close()


def test_a_revoked_key_is_a_provider_error_and_still_logged() -> None:
    server = MockServer(lambda _request: Response(401))
    logs = Logs()
    provider = Provider(SDK_KEY, server.url, stream=False, logger=logs, ready_timeout=0.05)
    try:
        # Listening before the provider is set, through the API: the refusal may come while the
        # provider initializes, before an OpenFeature SDK older than 0.10 binds it to its domain.
        heard = Heard(None)
        register(provider, "revoked")
        wait_until(lambda: ProviderEvent.PROVIDER_ERROR in heard.kinds())
        (details,) = [d for e, d in heard.events if e is ProviderEvent.PROVIDER_ERROR]
        assert details.error_code == ErrorCode.GENERAL
        assert details.message == "flagnetic: sdk key revoked"
        # The application's own logger still hears everything.
        wait_until(lambda: logs.count("key_revoked") == 1)
    finally:
        server.close()


def test_a_revoked_key_is_a_provider_error_even_when_the_logger_raises() -> None:
    server = MockServer(lambda _request: Response(401))

    def raising(_level: int, _event: str, _fields: Mapping[str, Any]) -> None:
        raise RuntimeError("logger broke")

    provider = Provider(SDK_KEY, server.url, stream=False, logger=raising, ready_timeout=0.05)
    try:
        heard = Heard(None)
        register(provider, "revoked-raising")
        wait_until(lambda: ProviderEvent.PROVIDER_ERROR in heard.kinds())
    finally:
        server.close()


def test_shutdown_closes_the_client_and_silences_events(offline: Any) -> None:
    provider, client = offline(allow_overrides=True)
    heard = Heard(client)
    provider.shutdown()
    provider.client.local_overrides.set("greeting", "after")
    assert ProviderEvent.PROVIDER_CONFIGURATION_CHANGED not in heard.kinds()


def test_initialize_waits_for_ready_and_never_fails() -> None:
    release = threading.Event()

    def handler(_request: Request) -> Response:
        release.wait(5)
        return Response(500)

    server = MockServer(handler)
    provider = Provider(SDK_KEY, server.url, logger=_quiet, ready_timeout=0.1)
    try:
        client = register(provider, "slow")
        assert client.get_provider_status() is ProviderStatus.READY
        assert provider.client.stage is flagnetic.Stage.BUILT_IN
    finally:
        release.set()
        server.close()


def test_a_client_key_is_refused_as_the_client_refuses_it() -> None:
    with pytest.raises(ValueError, match="server key"):
        Provider("fn_cli_abc", "https://flags.example.com")
