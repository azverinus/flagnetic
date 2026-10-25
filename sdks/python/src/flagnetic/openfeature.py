"""The OpenFeature provider for Flagnetic, installed with the extra: ``pip install 'flagnetic[openfeature]'``.

``Provider`` builds and owns a ``flagnetic.Client`` and serves its values to
the OpenFeature API. Reasons map as every Flagnetic provider maps them, in
every language; the Flagnetic reason itself, which OpenFeature's coarser one
cannot tell apart, travels in the flag metadata under ``flagnetic_reason``.
Exposures and aliases go through ``track``.
"""

from __future__ import annotations

import logging
import threading
from collections.abc import Mapping, Sequence
from typing import Any

try:
    from openfeature.evaluation_context import EvaluationContext
    from openfeature.event import ProviderEventDetails
    from openfeature.exception import ErrorCode
    from openfeature.flag_evaluation import FlagMetadata, FlagResolutionDetails, FlagValueType, Reason
    from openfeature.provider import AbstractProvider, Metadata
    from openfeature.track import TrackingEventDetails
except ImportError as err:  # pragma: no cover - exercised in a subprocess without the extra
    raise ImportError(
        "flagnetic.openfeature needs the OpenFeature SDK: pip install 'flagnetic[openfeature]'"
    ) from err

from flagnetic import _events
from flagnetic._client import Client
from flagnetic._detail import Detail, Stage
from flagnetic._eval import Context
from flagnetic._events import Logger, standard_logger

__all__ = ["REASON_METADATA_KEY", "Provider", "reason_of"]

#: The flag metadata key carrying the Flagnetic evaluation reason: a rule and a failed prerequisite
#: are both TARGETING_MATCH to OpenFeature.
REASON_METADATA_KEY = "flagnetic_reason"

_PROVIDER_NAME = "flagnetic"

# Every Flagnetic evaluation reason and its OpenFeature reason, identical in every provider of every
# language, so dashboards agree across services. targeting_match is what a client key is told in
# place of the reasons targeting decided, and error is the evaluator's reason a getter turns into
# fallback.
_REASONS: Mapping[str, Reason] = {
    "rule": Reason.TARGETING_MATCH,
    "prerequisite_failed": Reason.TARGETING_MATCH,
    "targeting_match": Reason.TARGETING_MATCH,
    "override": Reason.TARGETING_MATCH,
    "rollout": Reason.SPLIT,
    "experiment": Reason.SPLIT,
    "fallthrough": Reason.DEFAULT,
    "off": Reason.DISABLED,
    "error": Reason.ERROR,
    "fallback": Reason.ERROR,
    "local_override": Reason.STATIC,
}

_FALLBACK = "fallback"

# The tracking event names every Flagnetic provider accepts, and the attributes they read.
_TRACK_EXPOSURE = "exposure"
_TRACK_ALIAS = "alias"
_ALIAS_ATTRIBUTES = ("fromUnitType", "fromUnitId", "toUnitType", "toUnitId")

# The evaluation-context attribute carrying further unit IDs by unit type, beside the targeting key.
_ATTRIBUTE_UNITS = "units"


def reason_of(reason: str) -> Reason:
    """The OpenFeature reason of a Flagnetic evaluation reason.

    A reason a newer server adds is UNKNOWN rather than passed off as a known
    one; the Flagnetic reason still travels in the flag metadata.
    """
    return _REASONS.get(reason, Reason.UNKNOWN)


def _error_code(code: str) -> ErrorCode:
    """FLAG_NOT_FOUND and TYPE_MISMATCH keep their identity; every other fallback cause is GENERAL."""
    if code == "flag_not_found":
        return ErrorCode.FLAG_NOT_FOUND
    if code == "type_mismatch":
        return ErrorCode.TYPE_MISMATCH
    return ErrorCode.GENERAL


def _metadata(detail: Detail) -> FlagMetadata:
    """The detail's reason, stage, ruleset version, variation, platform override and experiment block."""
    metadata: dict[str, bool | int | float | str] = {
        REASON_METADATA_KEY: detail.reason,
        "stage": str(detail.stage),
        "ruleset_version": detail.ruleset_version,
        "variation": detail.variation,
        "overridden": detail.overridden,
    }
    experiment = detail.experiment
    if experiment is not None:
        metadata["experiment_key"] = experiment.key
        metadata["experiment_variation"] = experiment.variation
        metadata["experiment_unit_type"] = experiment.unit_type
        metadata["experiment_unit_id"] = experiment.unit_id
        metadata["experiment_inclusion_bucket"] = experiment.inclusion_bucket
        metadata["experiment_variation_bucket"] = experiment.variation_bucket
        metadata["experiment_override"] = experiment.override
    return metadata


def _resolution(detail: Detail) -> FlagResolutionDetails[Any]:
    """A fallback is an OpenFeature ERROR with its mapped code; else the mapped reason and variant."""
    if detail.reason == _FALLBACK:
        return FlagResolutionDetails(
            value=detail.value,
            error_code=_error_code(detail.error_code),
            error_message=detail.error_code,
            reason=Reason.ERROR,
            flag_metadata=_metadata(detail),
        )
    return FlagResolutionDetails(
        value=detail.value,
        reason=reason_of(detail.reason),
        variant=detail.variation or None,
        flag_metadata=_metadata(detail),
    )


class Provider(AbstractProvider):
    """An OpenFeature provider serving the values of a ``flagnetic.Client`` it builds and owns.

    ``sdk_key``, ``base_url`` and every further keyword option go to
    ``flagnetic.Client`` as they are, ``offline`` for tests included. The
    targeting key is the unit ID of ``unit_type`` (default ``user``); the
    ``units`` attribute, a mapping of unit type to unit ID, adds further units;
    every other attribute is an attribute of the context.

    ``initialize`` waits for the client's first fetch, at most
    ``ready_timeout`` seconds, and never fails: at stage ``built_in`` the
    fallback chain still serves the built-in defaults.

    Events: CONFIGURATION_CHANGED with the keys of the flags that may have
    changed; STALE when the client moves to stage ``last_good``; READY when it
    is fresh again; ERROR when the SDK key is revoked. A ``logger`` option
    keeps receiving every log event.

    ``track("exposure", context, details)`` reports the exposure of the flag
    named by the ``flag`` attribute for that context, as ``Client.exposure``
    does; ``track("alias", context, details)`` records the alias named by the
    ``fromUnitType``, ``fromUnitId``, ``toUnitType`` and ``toUnitId``
    attributes. An event naming neither, or missing what its mapping needs,
    does nothing. ``client`` is the wrapped client, for what OpenFeature
    cannot reach.
    """

    def __init__(
        self,
        sdk_key: str,
        base_url: str,
        *,
        unit_type: str = "user",
        ready_timeout: float = 10.0,
        **options: Any,
    ) -> None:
        super().__init__()
        self._unit_type = unit_type or "user"
        self._ready_timeout = ready_timeout
        self._closed = threading.Event()
        # Set by a revoked SDK key until the client is fresh again.
        self._revoked = False
        logger: Logger | None = options.pop("logger", None)
        self._logger: Logger = logger if logger is not None else standard_logger
        self._client = Client(sdk_key, base_url, logger=self._observe, **options)
        self._unsubscribe = (
            self._client.on_change(self._on_change),
            self._client.on_stage(self._on_stage),
        )

    @property
    def client(self) -> Client:
        """The wrapped client: local overrides, the recorder of an offline client, flush and status."""
        return self._client

    def get_metadata(self) -> Metadata:
        return Metadata(name=_PROVIDER_NAME)

    # -- lifecycle --------------------------------------------------------

    def initialize(self, evaluation_context: EvaluationContext) -> None:
        self._client.ready(self._ready_timeout)

    def shutdown(self) -> None:
        """Closes the wrapped client, which reports what is queued one last time; no event fires after it."""
        self._closed.set()
        for unsubscribe in self._unsubscribe:
            unsubscribe()
        self._client.close()

    # -- evaluation -------------------------------------------------------

    def resolve_boolean_details(
        self, flag_key: str, default_value: bool, evaluation_context: EvaluationContext | None = None
    ) -> FlagResolutionDetails[bool]:
        return _resolution(self._detail(flag_key, default_value, evaluation_context))

    def resolve_string_details(
        self, flag_key: str, default_value: str, evaluation_context: EvaluationContext | None = None
    ) -> FlagResolutionDetails[str]:
        return _resolution(self._detail(flag_key, default_value, evaluation_context))

    def resolve_integer_details(
        self, flag_key: str, default_value: int, evaluation_context: EvaluationContext | None = None
    ) -> FlagResolutionDetails[int]:
        # An int default reads the flag as get_int does: a number that is not whole or does not fit
        # in 64 bits serves the default with TYPE_MISMATCH rather than a truncated value.
        return _resolution(self._detail(flag_key, int(default_value), evaluation_context))

    def resolve_float_details(
        self, flag_key: str, default_value: float, evaluation_context: EvaluationContext | None = None
    ) -> FlagResolutionDetails[float]:
        return _resolution(self._detail(flag_key, float(default_value), evaluation_context))

    def resolve_object_details(
        self,
        flag_key: str,
        default_value: Sequence[FlagValueType] | Mapping[str, FlagValueType],
        evaluation_context: EvaluationContext | None = None,
    ) -> FlagResolutionDetails[Sequence[FlagValueType] | Mapping[str, FlagValueType]]:
        # A JSON value is served as a copy; a fallback serves the default itself.
        return _resolution(self._detail(flag_key, default_value, evaluation_context))

    def _detail(self, flag_key: str, default: Any, evaluation_context: EvaluationContext | None) -> Detail:
        return self._client.get_detail(self._context(evaluation_context), flag_key, default)

    def _context(self, evaluation_context: EvaluationContext | None) -> Context:
        """The flag context of an OpenFeature evaluation context."""
        if evaluation_context is None:
            return Context()
        units: dict[str, str] = {}
        attributes: dict[str, Any] = {}
        for key, value in evaluation_context.attributes.items():
            if key == _ATTRIBUTE_UNITS:
                if isinstance(value, Mapping):
                    units.update(
                        (unit_type, unit_id)
                        for unit_type, unit_id in value.items()
                        if isinstance(unit_type, str) and isinstance(unit_id, str)
                    )
            else:
                attributes[key] = value
        if evaluation_context.targeting_key:
            units[self._unit_type] = evaluation_context.targeting_key
        return Context(units=units, attributes=attributes)

    # -- track ------------------------------------------------------------

    def track(
        self,
        tracking_event_name: str,
        evaluation_context: EvaluationContext | None = None,
        tracking_event_details: TrackingEventDetails | None = None,
    ) -> None:
        attributes = tracking_event_details.attributes if tracking_event_details is not None else {}
        if tracking_event_name == _TRACK_EXPOSURE:
            flag = attributes.get("flag")
            if isinstance(flag, str) and flag:
                self._client.exposure(self._context(evaluation_context), flag)
        elif tracking_event_name == _TRACK_ALIAS:
            units = [attributes.get(name) for name in _ALIAS_ATTRIBUTES]
            if all(isinstance(unit, str) and unit for unit in units):
                from_type, from_id, to_type, to_id = (str(unit) for unit in units)
                self._client.alias(from_type, from_id, to_type, to_id)

    # -- events -----------------------------------------------------------

    def _on_change(self, changed: list[str]) -> None:
        details = ProviderEventDetails(flags_changed=changed)
        self._emit_if_open(self.emit_provider_configuration_changed, details)

    def _on_stage(self, stage: Stage) -> None:
        # Ready on every move to fresh, so the OpenFeature status leaves STALE when the client
        # recovers; the first fetch may also land while initialize waits, a ready the OpenFeature
        # SDK repeats once initialize returns.
        # A revoked key moves a fresh client to last_good right after the ERROR it fired: STALE would
        # hide the ERROR, so it is not sent while the key stays revoked.
        if stage is Stage.LAST_GOOD and not self._revoked:
            details = ProviderEventDetails(message="flagnetic: ruleset stage is last_good")
            self._emit_if_open(self.emit_provider_stale, details)
        elif stage is Stage.FRESH:
            self._revoked = False
            self._emit_if_open(self.emit_provider_ready, ProviderEventDetails())

    def _observe(self, level: int, event: str, fields: Mapping[str, Any]) -> None:
        """The client's logger: the application's own, plus ERROR when the SDK key is revoked.

        A revoked key is the one fetch failure stage last_good alone does not
        tell from an ordinary outage.
        """
        # The event goes first: the client swallows an exception from its logger, which would
        # otherwise also swallow the event.
        if event == _events.KEY_REVOKED:
            self._revoked = True
            details = ProviderEventDetails(message="flagnetic: sdk key revoked", error_code=ErrorCode.GENERAL)
            self._emit_if_open(self.emit_provider_error, details)
        self._logger(level, event, fields)

    def _emit_if_open(self, emit: Any, details: ProviderEventDetails) -> None:
        if self._closed.is_set():
            return
        try:
            emit(details)
        except Exception as err:
            self._logger(logging.ERROR, _events.LISTENER_FAILED, {"cause": repr(err)})
