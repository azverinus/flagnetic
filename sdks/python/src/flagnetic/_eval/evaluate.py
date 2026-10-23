"""Evaluating a flag of a decoded ruleset for a context."""

from __future__ import annotations

from typing import Any

from .bucket import bucket
from .model import Context, ExperimentDetail, Result
from .ruleset import (
    CONDITION_ATTRIBUTE,
    CONDITION_IN_SEGMENT,
    SERVE_EXPERIMENT,
    SERVE_ROLLOUT,
    SERVE_VARIATION,
    Condition,
    Flag,
    Ruleset,
    Serve,
)

REASON_OFF = "off"
REASON_OVERRIDE = "override"
REASON_RULE = "rule"
REASON_ROLLOUT = "rollout"
REASON_EXPERIMENT = "experiment"
REASON_FALLTHROUGH = "fallthrough"
REASON_PREREQUISITE_FAILED = "prerequisite_failed"
REASON_ERROR = "error"

ERROR_FLAG_NOT_FOUND = "flag_not_found"
ERROR_PREREQUISITE_CYCLE = "prerequisite_cycle"
ERROR_INCONSISTENT_RULESET = "inconsistent_ruleset"


def evaluate(ruleset: Ruleset, context: Context, flag: str) -> Result:
    """Computes one flag for a context.

    It never reads a clock, never records an exposure and never fails:
    problems are results with reason error.
    """
    return _Evaluation(ruleset, context).flag(flag)


def _error(code: str, influenced: bool) -> Result:
    return Result(reason=REASON_ERROR, error_code=code, override_influenced=influenced)


class _Evaluation:
    """The state of one evaluation: the flags on the current prerequisite path and the results so far."""

    __slots__ = ("attributes", "memo", "ruleset", "units", "visiting")

    def __init__(self, ruleset: Ruleset, context: Context) -> None:
        self.ruleset = ruleset
        self.units = context.units
        self.attributes = context.attributes
        self.visiting: set[str] = set()
        self.memo: dict[str, Result] = {}

    def unit(self, unit_type: str) -> str | None:
        """The unit ID of a unit type, or None: an empty unit ID counts as absent."""
        unit_id = self.units.get(unit_type)
        return unit_id if isinstance(unit_id, str) and unit_id != "" else None

    def flag(self, key: str) -> Result:
        """Evaluates a flag by key. A flag reached again on the current prerequisite path is a cycle."""
        memoized = self.memo.get(key)
        if memoized is not None:
            return memoized
        f = self.ruleset.flags.get(key)
        if f is None:
            return _error(ERROR_FLAG_NOT_FOUND, False)
        if key in self.visiting:
            return _of_flag(f, _error(ERROR_PREREQUISITE_CYCLE, False))
        self.visiting.add(key)
        result = _of_flag(f, self.compute(f))
        self.visiting.discard(key)
        self.memo[key] = result
        return result

    def compute(self, f: Flag) -> Result:
        if not f.on:
            return self.serve(f, f.off_variation, Result(reason=REASON_OFF))
        # Overrides, then the same steps in shadow for experiment detail.
        for unit_type, unit_id, variation in f.overrides:
            if self.unit(unit_type) != unit_id:
                continue
            result = self.serve(f, variation, Result(reason=REASON_OVERRIDE, override_influenced=True))
            if result.reason == REASON_ERROR:
                return result
            shadow = self.body(f)
            if shadow.reason == REASON_EXPERIMENT and shadow.experiment is not None:
                detail = shadow.experiment
                result.experiment = ExperimentDetail(
                    key=detail.key,
                    variation=result.variation or "",
                    unit_type=detail.unit_type,
                    unit_id=detail.unit_id,
                    inclusion_salt=detail.inclusion_salt,
                    variation_salt=detail.variation_salt,
                    inclusion_bucket=detail.inclusion_bucket,
                    variation_bucket=detail.variation_bucket,
                    override=True,
                )
            return result
        return self.body(f)

    def body(self, f: Flag) -> Result:
        """Prerequisites, rules and the fallthrough."""
        influenced = [False]

        # Flag-level prerequisites in order; the first failure serves the off variation.
        for flag_key, variation in f.prerequisites:
            held, code = self.prerequisite(flag_key, variation, influenced)
            if code:
                return _error(code, influenced[0])
            if not held:
                return self.serve(
                    f,
                    f.off_variation,
                    Result(
                        reason=REASON_PREREQUISITE_FAILED,
                        prerequisite=flag_key,
                        override_influenced=influenced[0],
                    ),
                )

        # Rules in order; conditions in order, stopping at the first false.
        for rule in f.rules:
            matched, code = self.conditions(rule.conditions, influenced)
            if code:
                return _error(code, influenced[0])
            if not matched:
                continue
            if rule.invalid_serve:
                return _error(ERROR_INCONSISTENT_RULESET, influenced[0])
            s = rule.serve
            if s.kind == SERVE_VARIATION:
                return self.serve(
                    f,
                    s.variation,
                    Result(reason=REASON_RULE, rule_id=rule.id, override_influenced=influenced[0]),
                )
            if s.kind == SERVE_ROLLOUT:
                unit_id = self.unit(s.unit_type)
                if unit_id is None:
                    continue
                b = bucket(s.salt, unit_id)
                if b >= s.percentage:
                    continue
                return self.serve(
                    f,
                    s.variation,
                    Result(
                        reason=REASON_ROLLOUT, rule_id=rule.id, bucket=b, override_influenced=influenced[0]
                    ),
                )
            if s.kind == SERVE_EXPERIMENT:
                unit_id = self.unit(s.unit_type)
                if unit_id is None:
                    continue
                inclusion = bucket(s.inclusion_salt, unit_id)
                if inclusion >= s.traffic_allocation:
                    continue
                variation_bucket = bucket(s.variation_salt, unit_id)
                assigned = _assign(s, variation_bucket)
                if assigned is None:
                    return _error(ERROR_INCONSISTENT_RULESET, influenced[0])
                result = self.serve(
                    f,
                    assigned,
                    Result(reason=REASON_EXPERIMENT, rule_id=rule.id, override_influenced=influenced[0]),
                )
                if result.reason == REASON_ERROR:
                    return result
                result.experiment = ExperimentDetail(
                    key=s.experiment,
                    variation=assigned,
                    unit_type=s.unit_type,
                    unit_id=unit_id,
                    inclusion_salt=s.inclusion_salt,
                    variation_salt=s.variation_salt,
                    inclusion_bucket=inclusion,
                    variation_bucket=variation_bucket,
                    override=influenced[0],
                )
                return result
            return _error(ERROR_INCONSISTENT_RULESET, influenced[0])

        return self.serve(
            f, f.fallthrough, Result(reason=REASON_FALLTHROUGH, override_influenced=influenced[0])
        )

    def serve(self, f: Flag, variation: str, result: Result) -> Result:
        """Fills result with a variation of f, or turns it into inconsistent_ruleset when it is missing."""
        if variation not in f.variations:
            return _error(ERROR_INCONSISTENT_RULESET, result.override_influenced)
        result.value = f.variations[variation]
        result.variation = variation
        return result

    def prerequisite(self, key: str, variation: str, influenced: list[bool]) -> tuple[bool, str]:
        """Evaluates another flag for the same context.

        It holds when that flag's reason is neither off nor error and it serves
        the required variation. An error in the prerequisite is a defect of the
        ruleset and is returned, so it surfaces on the dependent flag instead
        of silently serving its off variation. Influence is accumulated into
        influenced.
        """
        f = self.ruleset.flags.get(key)
        if f is None or variation not in f.variations:
            return False, ERROR_INCONSISTENT_RULESET
        result = self.flag(key)
        influenced[0] = influenced[0] or result.override_influenced
        if result.reason == REASON_ERROR:
            return False, result.error_code
        return result.reason != REASON_OFF and result.variation == variation, ""

    def conditions(self, conditions: tuple[Condition, ...], influenced: list[bool]) -> tuple[bool, str]:
        """Evaluates an AND-set in order, stopping at the first false condition.

        Later conditions are not evaluated and cannot influence the result.
        """
        for c in conditions:
            held, code = self.condition(c, influenced)
            if code or not held:
                return False, code
        return True, ""

    def condition(self, c: Condition, influenced: list[bool]) -> tuple[bool, str]:
        if c.broken:
            return False, ERROR_INCONSISTENT_RULESET
        if c.kind == CONDITION_ATTRIBUTE:
            value: Any = self.unit(c.attribute) if c.unit else self.attributes.get(c.attribute)
            assert c.match is not None
            return c.match(value), ""
        if c.kind in (CONDITION_IN_SEGMENT, "not_in_segment"):
            segment = self.ruleset.segments.get(c.segment)
            if segment is None:
                return False, ERROR_INCONSISTENT_RULESET
            inside, code = self.conditions(segment.conditions, influenced)
            if code:
                return False, code
            return inside == (c.kind == CONDITION_IN_SEGMENT), ""
        return self.prerequisite(c.flag, c.variation, influenced)


def _of_flag(f: Flag, result: Result) -> Result:
    """Stamps a result with the flag it is about, whatever reason a known flag carries."""
    result.type = f.type
    result.automatic_exposure = f.automatic_exposure
    return result


def _assign(serve: Serve, bucket_: int) -> str | None:
    """Walks the weights in list order; variation i covers [sum(w_0..w_i-1), sum(w_0..w_i))."""
    upper = 0
    for variation, weight in serve.weights:
        upper += weight
        if bucket_ < upper:
            return variation
    return None
