"""Decoding a ruleset document into what the evaluator walks."""

from __future__ import annotations

import json
from collections.abc import Mapping
from typing import Any

from .model import Construct, ConstructKind
from .operators import OPERATORS, Matcher, OperatorError, compile_operator

SCHEMA_VERSION = 1
SCOPE_SERVER = "server"
BUCKETS = 10_000

CONDITION_ATTRIBUTE = "attribute"
CONDITION_IN_SEGMENT = "in_segment"
CONDITION_NOT_IN_SEGMENT = "not_in_segment"
CONDITION_PREREQUISITE = "prerequisite"
_CONDITION_KINDS = frozenset(
    {CONDITION_ATTRIBUTE, CONDITION_IN_SEGMENT, CONDITION_NOT_IN_SEGMENT, CONDITION_PREREQUISITE}
)

SERVE_VARIATION = "variation"
SERVE_ROLLOUT = "rollout"
SERVE_EXPERIMENT = "experiment"
_SERVE_KINDS = frozenset({SERVE_VARIATION, SERVE_ROLLOUT, SERVE_EXPERIMENT})


class RulesetError(ValueError):
    """The document is not a ruleset this evaluator can read."""


class Condition:
    """One comparison, segment membership or prerequisite, prepared once.

    ``broken`` is set when the condition cannot be evaluated against this
    document; evaluating it then yields inconsistent_ruleset.
    """

    __slots__ = (
        "attribute",
        "broken",
        "flag",
        "kind",
        "match",
        "segment",
        "unit",
        "unsupported",
        "variation",
    )

    def __init__(self, kind: str) -> None:
        self.kind = kind
        self.broken = False
        # Set beside broken when the condition holds an operator or condition
        # kind this evaluator does not know.
        self.unsupported: Construct | None = None
        self.attribute = ""
        self.unit = False  # the attribute names a unit type: its value is the unit ID
        self.match: Matcher | None = None
        self.segment = ""
        self.flag = ""
        self.variation = ""


class Serve:
    """What a matching rule serves. Fields apply per kind."""

    __slots__ = (
        "experiment", "inclusion_salt", "kind", "percentage", "salt", "traffic_allocation",
        "unit_type", "variation", "variation_salt", "weights",
    )  # fmt: skip

    def __init__(self, raw: Mapping[str, Any], where: str) -> None:
        self.kind = _string(raw, "kind", where)
        self.variation = _string(raw, "variation", where)
        self.unit_type = _string(raw, "unit_type", where)
        self.percentage = _integer(raw, "percentage", where)
        self.salt = _string(raw, "salt", where)
        self.experiment = _string(raw, "experiment", where)
        self.inclusion_salt = _string(raw, "inclusion_salt", where)
        self.variation_salt = _string(raw, "variation_salt", where)
        self.traffic_allocation = _integer(raw, "traffic_allocation", where)
        self.weights = tuple(
            (_string(w, "variation", where), _integer(w, "weight", where))
            for w in _objects(raw, "weights", where)
        )


class Rule:
    __slots__ = ("conditions", "id", "invalid_serve", "serve")

    def __init__(self, id: str, conditions: tuple[Condition, ...], serve: Serve, invalid_serve: bool) -> None:
        self.id = id
        self.conditions = conditions
        self.serve = serve
        # The serve is an experiment breaking the invariants every experiment
        # serve holds, so evaluation reaching it answers inconsistent_ruleset
        # without checking them again.
        self.invalid_serve = invalid_serve


class Flag:
    """One flag with its configuration in the ruleset's environment."""

    __slots__ = (
        "automatic_exposure", "fallthrough", "key", "off_variation", "on", "overrides",
        "prerequisites", "rules", "type", "variations",
    )  # fmt: skip

    key: str
    type: str
    automatic_exposure: bool
    on: bool
    off_variation: str
    fallthrough: str
    variations: dict[str, Any]
    prerequisites: tuple[tuple[str, str], ...]
    overrides: tuple[tuple[str, str, str], ...]
    rules: tuple[Rule, ...]


class Segment:
    """A reusable, named AND-set of attribute conditions."""

    __slots__ = ("conditions", "unsupported")

    def __init__(self, conditions: tuple[Condition, ...], unsupported: list[Construct]) -> None:
        self.conditions = conditions
        self.unsupported = unsupported


class Ruleset:
    """The complete evaluable configuration of one environment at one moment.

    Decode a document with ``decode``. A ruleset is not changed once decoded,
    so any number of threads read it at once without a lock.
    """

    __slots__ = ("environment_key", "flags", "keys", "protected", "segments", "unsupported", "version")

    version: int
    environment_key: str
    protected: bool
    flags: dict[str, Flag]
    segments: dict[str, Segment]
    keys: tuple[str, ...]
    unsupported: dict[str, list[Construct]]

    def flag(self, key: str) -> Flag | None:
        """The flag with key, as the document has it, or None."""
        return self.flags.get(key)

    def unsupported_rules(self) -> dict[str, list[Construct]]:
        """The constructs each flag holds that this evaluator does not know, by flag key.

        In document order, each once. A flag holds what its rules hold and what
        the segments its rules name hold. Empty for a ruleset this evaluator
        fully supports.
        """
        return {key: list(constructs) for key, constructs in self.unsupported.items()}


# Go's int and int64, the widest integer a ruleset member holds: every
# evaluator refuses a document whose integer does not fit, as Go's decoder does.
_INT_MIN = -(1 << 63)
_INT_MAX = (1 << 63) - 1


def _object(value: Any, where: str) -> Mapping[str, Any]:
    # A null object reads as an empty one, every member at its zero value, as
    # Go's decoder reads it: the flag holding it then evaluates to
    # inconsistent_ruleset instead of the whole document being refused.
    if value is None:
        return {}
    if not isinstance(value, dict):
        raise RulesetError(f"decode ruleset: {where} is not an object")
    return value


def _objects(raw: Mapping[str, Any], key: str, where: str) -> list[Mapping[str, Any]]:
    value = raw.get(key)
    if value is None:
        return []
    if not isinstance(value, list):
        raise RulesetError(f"decode ruleset: {where}.{key} is not a list")
    return [_object(item, f"{where}.{key}[{i}]") for i, item in enumerate(value)]


def _string(raw: Mapping[str, Any], key: str, where: str) -> str:
    value = raw.get(key)
    if value is None:
        return ""
    if not isinstance(value, str):
        raise RulesetError(f"decode ruleset: {where}.{key} is not a string")
    return value


def _boolean(raw: Mapping[str, Any], key: str, where: str) -> bool:
    value = raw.get(key)
    if value is None:
        return False
    if not isinstance(value, bool):
        raise RulesetError(f"decode ruleset: {where}.{key} is not a boolean")
    return value


def _integer(raw: Mapping[str, Any], key: str, where: str) -> int:
    value = raw.get(key)
    if value is None:
        return 0
    if isinstance(value, bool) or not isinstance(value, int) or not _INT_MIN <= value <= _INT_MAX:
        raise RulesetError(f"decode ruleset: {where}.{key} is not an integer")
    return value


def _refuse_constant(name: str) -> Any:
    # NaN and Infinity are not JSON; Python's parser accepts them, Go's does not.
    raise ValueError(f"{name} is not JSON")


def parse(document: str | bytes | bytearray) -> Any:
    """Reads the JSON text of a ruleset document, as Go's decoder reads it.

    Raises RulesetError when the text is not JSON.
    """
    if not isinstance(document, str):
        # UTF-8 only, with no byte order mark and each invalid sequence
        # read as U+FFFD, as Go decodes a document; json.loads would
        # otherwise also guess UTF-16 and UTF-32 from the bytes.
        document = bytes(document).decode("utf-8", errors="replace")
    try:
        return json.loads(document, parse_constant=_refuse_constant)
    except (ValueError, RecursionError) as err:
        raise RulesetError(f"decode ruleset: {err}") from err


def decode(document: str | bytes | bytearray | Mapping[str, Any]) -> Ruleset:
    """Reads a ruleset document: JSON text, or the object it decodes to.

    Raises RulesetError only when the document is not a ruleset this evaluator
    can read: malformed JSON, a member of the wrong type, an unsupported
    schema_version or a scope other than server. Dangling references, invalid
    conditions and invalid experiment serves do not fail decoding; evaluating
    a flag that reaches any of them returns reason error with
    inconsistent_ruleset, so one defective flag never takes the others down.
    """
    raw: Any = parse(document) if isinstance(document, (str, bytes, bytearray)) else document
    top = _object(raw, "the document")
    schema_version = top.get("schema_version")
    if type(schema_version) is not int or schema_version != SCHEMA_VERSION:
        raise RulesetError(
            f"decode ruleset: unsupported schema_version {schema_version!r}, "
            f"this evaluator reads {SCHEMA_VERSION}"
        )
    scope = top.get("scope")
    if scope != SCOPE_SERVER:
        raise RulesetError(f"decode ruleset: scope {scope!r}, this evaluator reads {SCOPE_SERVER!r} only")

    rs = Ruleset()
    rs.version = _integer(top, "version", "the document")
    environment = top.get("environment")
    rs.environment_key = ""
    rs.protected = False
    if environment is not None:
        env = _object(environment, "environment")
        rs.environment_key = _string(env, "key", "environment")
        rs.protected = _boolean(env, "protected", "environment")

    types: dict[str, str] = {}
    for a in _objects(top, "attributes", "the document"):
        types[_string(a, "key", "attributes")] = _string(a, "type", "attributes")
    units: set[str] = set()
    unit_types = top.get("unit_types")
    if unit_types is not None:
        if not isinstance(unit_types, list) or not all(u is None or isinstance(u, str) for u in unit_types):
            raise RulesetError("decode ruleset: unit_types is not a list of strings")
        for u in unit_types:
            # A null unit type reads as "", as Go's decoder reads it.
            unit_type = u or ""
            units.add(unit_type)
            types[unit_type] = "string"
    _build(rs, top, types, units)
    return rs


def _add(found: list[Construct], construct: Construct | None) -> None:
    if construct is not None and construct not in found:
        found.append(construct)


def _unsupported(kind: ConstructKind, name: str) -> Construct | None:
    """The construct of a kind with a name this evaluator does not know.

    None for a missing name: no server sends that, so it is a defect of the
    ruleset rather than a reason to upgrade.
    """
    return Construct(kind, name) if name else None


def _compile_condition(
    raw: Mapping[str, Any], types: dict[str, str], units: set[str], where: str
) -> Condition:
    cond = Condition(_string(raw, "kind", where))
    if cond.kind == CONDITION_ATTRIBUTE:
        op = _string(raw, "op", where)
        if op not in OPERATORS:
            cond.broken = True
            cond.unsupported = _unsupported("operator", op)
            return cond
        attribute = _string(raw, "attribute", where)
        attribute_type = types.get(attribute)
        if attribute_type is None:
            cond.broken = True
            return cond
        try:
            cond.match = compile_operator(attribute_type, op, raw.get("value"))
        except OperatorError:
            cond.broken = True
            return cond
        cond.attribute = attribute
        cond.unit = attribute in units
    elif cond.kind in (CONDITION_IN_SEGMENT, CONDITION_NOT_IN_SEGMENT):
        cond.segment = _string(raw, "segment", where)
    elif cond.kind == CONDITION_PREREQUISITE:
        cond.flag = _string(raw, "flag", where)
        cond.variation = _string(raw, "variation", where)
    else:
        cond.broken = True
        cond.unsupported = _unsupported("condition_kind", cond.kind)
    return cond


def _valid_experiment(serve: Serve, variations: Mapping[str, Any]) -> bool:
    """Whether an experiment serve holds what assigning a unit to a variation relies on.

    An experiment key, a unit type and both salts; a traffic allocation within
    0 to 10000; and at least two weights, none negative, each naming a
    distinct variation of the flag, summing to exactly 10000. Any other serve
    would assign units to variations the experiment does not compare, or to
    none, so every evaluator refuses it the same way instead of each
    assigning its own way.
    """
    if (
        not serve.experiment
        or not serve.unit_type
        or not serve.inclusion_salt
        or not serve.variation_salt
        or serve.traffic_allocation < 0
        or serve.traffic_allocation > BUCKETS
        or len(serve.weights) < 2
    ):
        return False
    seen: set[str] = set()
    total = 0
    for variation, weight in serve.weights:
        if variation not in variations or variation in seen or weight < 0:
            return False
        seen.add(variation)
        total += weight
    return total == BUCKETS


def _build(rs: Ruleset, top: Mapping[str, Any], types: dict[str, str], units: set[str]) -> None:
    rs.segments = {}
    for raw in _objects(top, "segments", "the document"):
        key = _string(raw, "key", "segments")
        conditions: list[Condition] = []
        found: list[Construct] = []
        for c in _objects(raw, "conditions", f"segment {key!r}"):
            kind = _string(c, "kind", f"segment {key!r}")
            if kind != CONDITION_ATTRIBUTE:
                # A segment holds only attribute conditions; another known kind
                # there is a defect of the ruleset, an unknown one is an
                # unsupported construct.
                broken = Condition(kind)
                broken.broken = True
                conditions.append(broken)
                if kind not in _CONDITION_KINDS:
                    _add(found, _unsupported("condition_kind", kind))
                continue
            cond = _compile_condition(c, types, units, f"segment {key!r}")
            conditions.append(cond)
            _add(found, cond.unsupported)
        rs.segments[key] = Segment(tuple(conditions), found)

    rs.flags = {}
    rs.unsupported = {}
    keys: list[str] = []
    for raw in _objects(top, "flags", "the document"):
        f = Flag()
        f.key = _string(raw, "key", "flags")
        where = f"flag {f.key!r}"
        f.type = _string(raw, "type", where)
        f.automatic_exposure = _boolean(raw, "automatic_exposure", where)
        f.on = _boolean(raw, "on", where)
        f.off_variation = _string(raw, "off_variation", where)
        f.fallthrough = _string(raw, "fallthrough", where)
        f.variations = {}
        for v in _objects(raw, "variations", where):
            f.variations[_string(v, "key", where)] = v.get("value")
        f.prerequisites = tuple(
            (_string(p, "flag", where), _string(p, "variation", where))
            for p in _objects(raw, "prerequisites", where)
        )
        f.overrides = tuple(
            (_string(o, "unit_type", where), _string(o, "unit_id", where), _string(o, "variation", where))
            for o in _objects(raw, "overrides", where)
        )
        rules: list[Rule] = []
        found = []
        for r in _objects(raw, "rules", where):
            rule_where = f"{where} rule {_string(r, 'id', where)!r}"
            conditions = []
            for c in _objects(r, "conditions", rule_where):
                cond = _compile_condition(c, types, units, rule_where)
                conditions.append(cond)
                _add(found, cond.unsupported)
                segment = rs.segments.get(cond.segment) if cond.segment else None
                if segment is not None:
                    for construct in segment.unsupported:
                        _add(found, construct)
            serve = Serve(_object(r.get("serve"), f"{rule_where}.serve"), rule_where)
            invalid = serve.kind == SERVE_EXPERIMENT and not _valid_experiment(serve, f.variations)
            if serve.kind not in _SERVE_KINDS:
                _add(found, _unsupported("serve_kind", serve.kind))
            rules.append(Rule(_string(r, "id", where), tuple(conditions), serve, invalid))
        f.rules = tuple(rules)
        if found:
            rs.unsupported[f.key] = found
        rs.flags[f.key] = f
        keys.append(f.key)
    rs.keys = tuple(keys)
