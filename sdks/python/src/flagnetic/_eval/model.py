"""What the evaluator takes and gives."""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
from typing import Any, Literal, NamedTuple


@dataclass(frozen=True, slots=True)
class Context:
    """What a flag is evaluated for: unit IDs by unit type, and attributes.

    A context with no units is valid: attribute rules apply; rollouts,
    experiments and overrides are skipped. An empty unit ID counts as absent.

    Attribute values are a string, a boolean, a number, a list of strings, or
    for a date an RFC 3339 string, integral Unix epoch milliseconds or an
    aware ``datetime``. A boolean is never a number.
    """

    units: Mapping[str, str] = field(default_factory=dict)
    attributes: Mapping[str, Any] = field(default_factory=dict)


ConstructKind = Literal["operator", "condition_kind", "serve_kind"]


class Construct(NamedTuple):
    """An operator, condition kind or serve kind of a ruleset that this evaluator does not know."""

    kind: ConstructKind
    name: str


@dataclass(slots=True)
class ExperimentDetail:
    """Everything needed to report an exposure."""

    key: str
    variation: str
    unit_type: str
    unit_id: str
    inclusion_salt: str
    variation_salt: str
    inclusion_bucket: int
    variation_bucket: int
    override: bool

    def as_dict(self) -> dict[str, Any]:
        return {
            "key": self.key,
            "variation": self.variation,
            "unit_type": self.unit_type,
            "unit_id": self.unit_id,
            "inclusion_salt": self.inclusion_salt,
            "variation_salt": self.variation_salt,
            "inclusion_bucket": self.inclusion_bucket,
            "variation_bucket": self.variation_bucket,
            "override": self.override,
        }


@dataclass(slots=True)
class Result:
    """What evaluating one flag for one context served, and why.

    ``variation`` is None when none is served, for an error. ``type`` and
    ``automatic_exposure`` are the flag's, so they are None when there is no
    flag to read them from: an error with ``flag_not_found``. A result is not
    to be changed once returned.
    """

    reason: str
    type: str | None = None
    value: Any = None
    variation: str | None = None
    rule_id: str = ""
    automatic_exposure: bool | None = None
    experiment: ExperimentDetail | None = None
    prerequisite: str = ""
    bucket: int | None = None
    error_code: str = ""
    override_influenced: bool = False

    def as_dict(self) -> dict[str, Any]:
        """The result as the conformance fixtures compare it, key for key."""
        out: dict[str, Any] = {}
        if self.type is not None:
            out["type"] = self.type
        if self.variation is not None:
            out["value"] = self.value
            out["variation"] = self.variation
        out["reason"] = self.reason
        if self.rule_id:
            out["rule_id"] = self.rule_id
        if self.automatic_exposure is not None:
            out["automatic_exposure"] = self.automatic_exposure
        if self.experiment is not None:
            out["experiment"] = self.experiment.as_dict()
        if self.prerequisite:
            out["prerequisite"] = self.prerequisite
        if self.bucket is not None:
            out["bucket"] = self.bucket
        if self.error_code:
            out["error_code"] = self.error_code
        out["override_influenced"] = self.override_influenced
        return out
