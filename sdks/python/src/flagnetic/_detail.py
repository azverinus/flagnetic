"""What a client reports about the values it serves and the state it serves them in."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from enum import StrEnum
from typing import Any


class Stage(StrEnum):
    """The step of the fallback chain a client evaluates against."""

    #: No ruleset was ever received: getters serve built-in defaults.
    BUILT_IN = "built_in"
    #: The held ruleset came from an earlier success, or from the store, and the most recent attempt failed.
    LAST_GOOD = "last_good"
    #: The most recent attempt succeeded, a 304 included.
    FRESH = "fresh"


@dataclass(frozen=True, slots=True)
class Status:
    """The state of the fallback chain.

    ``stale_since`` is when the first fetch failure since the last success
    happened; None after a success and before any attempt.
    """

    stage: Stage
    stale_since: datetime | None = None


@dataclass(frozen=True, slots=True)
class Experiment:
    """The experiment block of a detail: the facts an exposure of the value is made of.

    ``variation`` is the served variation: the assignment, or the override's
    variation when an override decided the value. ``override`` is true when an
    override influenced the value, directly or through a prerequisite;
    exposures carrying it are excluded from analysis.
    """

    key: str
    variation: str
    unit_type: str
    unit_id: str
    inclusion_salt: str
    variation_salt: str
    inclusion_bucket: int
    variation_bucket: int
    override: bool


@dataclass(frozen=True, slots=True)
class Detail:
    """A served value and why it was served.

    ``value`` is a bool, str, float or int by the getter, or a JSON value
    that is the caller's to modify; with reason ``fallback`` it is the
    built-in default as passed. ``reason`` is one of ``rule``, ``rollout``,
    ``experiment``, ``override``, ``prerequisite_failed``, ``off``,
    ``fallthrough``, ``local_override`` and ``fallback``; the evaluator's own
    ``error`` is never returned, an evaluator error serves the default with
    reason ``fallback`` and the evaluator's error code. ``error_code`` is set
    with ``fallback``: ``flag_not_found``, ``prerequisite_cycle``,
    ``inconsistent_ruleset``, ``type_mismatch``, ``no_ruleset`` (stage
    ``built_in``, nothing to evaluate against) or ``general`` (an internal
    failure caught at the API boundary).

    ``ruleset_version`` and ``received_at`` describe the ruleset evaluated
    against, 0 and None at stage ``built_in``; the age of a last good ruleset
    shows in ``received_at``. ``overridden`` is true when a platform override
    decided the value, directly or through a prerequisite. ``experiment`` is
    present when the value came from a running experiment.
    """

    value: Any
    reason: str
    variation: str = ""
    rule_id: str = ""
    error_code: str = ""
    ruleset_version: int = 0
    received_at: datetime | None = None
    stage: Stage = Stage.BUILT_IN
    overridden: bool = False
    experiment: Experiment | None = None
