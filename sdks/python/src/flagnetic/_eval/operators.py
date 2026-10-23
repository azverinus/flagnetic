"""Conditions compiled once, when a ruleset is decoded."""

from __future__ import annotations

import datetime
import math
from collections.abc import Callable
from typing import Any

from . import _re2
from .instant import from_epoch_millis, parse_rfc3339
from .semver import Semver, parse_semver

Matcher = Callable[[Any], bool]

# Every operator this evaluator knows, for any attribute type. An operator
# outside it is an unsupported construct, typically sent by a server newer than
# the evaluator; a known operator applied to a type it is not defined for is a
# defect of the ruleset instead.
OPERATORS = frozenset(
    {
        "eq", "neq", "lt", "lte", "gt", "gte",
        "in", "not_in", "contains", "not_contains",
        "starts_with", "ends_with", "matches",
        "before", "after",
        "contains_any", "contains_all", "is_empty",
        "exists", "not_exists",
    }
)  # fmt: skip

ATTRIBUTE_TYPES = frozenset({"string", "number", "boolean", "semver", "date", "list"})


class OperatorError(ValueError):
    """The operator cannot be applied to the attribute type with this value."""


def to_number(value: Any) -> float | None:
    """A number as a double. A boolean and a string are not numbers."""
    if isinstance(value, bool):
        return None
    if isinstance(value, float):
        number = value
    elif isinstance(value, int):
        try:
            number = float(value)
        except OverflowError:
            return None
    else:
        return None
    if math.isnan(number) or math.isinf(number):
        return None
    return number


def to_strings(value: Any) -> list[str] | None:
    """A list whose every element is a string."""
    if isinstance(value, (list, tuple)) and all(isinstance(e, str) for e in value):
        return list(value)
    return None


def to_instant(value: Any) -> int | None:
    """An RFC 3339 string with an offset, an aware datetime or integral Unix epoch milliseconds."""
    if isinstance(value, str):
        return parse_rfc3339(value)
    if isinstance(value, datetime.datetime):
        if value.tzinfo is None or value.utcoffset() is None:
            return None
        delta = value - datetime.datetime(1970, 1, 1, tzinfo=datetime.UTC)
        return (delta.days * 86400 + delta.seconds) * 1_000_000_000 + delta.microseconds * 1000
    ms = to_number(value)
    if ms is None:
        return None
    return from_epoch_millis(ms)


def _extract_semver(value: Any) -> Semver | None:
    return parse_semver(value) if isinstance(value, str) else None


def _extract_string(value: Any) -> str | None:
    return value if isinstance(value, str) else None


def _extract_boolean(value: Any) -> bool | None:
    return value if isinstance(value, bool) else None


_EXTRACTORS: dict[str, Callable[[Any], Any]] = {
    "string": _extract_string,
    "number": to_number,
    "boolean": _extract_boolean,
    "semver": _extract_semver,
    "date": to_instant,
    "list": to_strings,
}


def compile_operator(attribute_type: str, op: str, value: Any) -> Matcher:
    """A matcher of a context value for one operator of one attribute type.

    The matcher receives None for an absent attribute. An absent, null or
    wrong-typed value makes every operator false except not_exists, which is
    then true; there is no coercion. Raises OperatorError when the operator is
    not defined for the type or the value has the wrong JSON type.
    """
    extract = _EXTRACTORS.get(attribute_type)
    if extract is None:
        raise OperatorError(f"unknown attribute type {attribute_type!r}")
    if op == "exists":
        return lambda v: extract(v) is not None
    if op == "not_exists":
        return lambda v: extract(v) is None
    test = _operator_test(attribute_type, op, value)

    def match(v: Any) -> bool:
        x = extract(v)
        return x is not None and test(x)

    return match


def _compare(op: str, cmp: Callable[[Any], int]) -> Callable[[Any], bool]:
    """eq, neq, lt, lte, gt or gte over a three-way comparison of the context value."""
    if op == "eq":
        return lambda x: cmp(x) == 0
    if op == "neq":
        return lambda x: cmp(x) != 0
    if op == "lt":
        return lambda x: cmp(x) < 0
    if op == "lte":
        return lambda x: cmp(x) <= 0
    if op == "gt":
        return lambda x: cmp(x) > 0
    return lambda x: cmp(x) >= 0


def _never(_: Any) -> bool:
    return False


def _operator_test(attribute_type: str, op: str, value: Any) -> Callable[[Any], bool]:
    """Compiles the condition value of one operator into a test of an already typed context value."""
    if attribute_type == "string":
        return _string_test(op, value)
    if attribute_type == "number":
        return _number_test(op, value)
    if attribute_type == "boolean":
        if op == "eq":
            if not isinstance(value, bool):
                raise OperatorError(f"operator {op!r} needs a boolean value")
            return lambda x: x == value
    elif attribute_type == "semver":
        return _semver_test(op, value)
    elif attribute_type == "date":
        return _date_test(op, value)
    else:
        return _list_test(op, value)
    raise OperatorError(f"operator {op!r} is not defined for type {attribute_type}")


def _string_test(op: str, value: Any) -> Callable[[Any], bool]:
    if op in ("eq", "neq", "contains", "not_contains", "starts_with", "ends_with"):
        if not isinstance(value, str):
            raise OperatorError(f"operator {op!r} needs a string value")
        want = value
        if op in ("eq", "neq"):
            return (lambda x: x == want) if op == "eq" else (lambda x: x != want)
        if op in ("contains", "not_contains"):
            return (lambda x: want in x) if op == "contains" else (lambda x: want not in x)
        if op == "starts_with":
            return lambda x: x.startswith(want)
        return lambda x: x.endswith(want)
    if op in ("in", "not_in"):
        strings = to_strings(value)
        if strings is None:
            raise OperatorError(f"operator {op!r} needs a list of strings")
        members = frozenset(strings)
        return (lambda x: x in members) if op == "in" else (lambda x: x not in members)
    if op == "matches":
        if not isinstance(value, str):
            raise OperatorError(f"operator {op!r} needs a string value")
        try:
            return _re2.compile_pattern(value)
        except _re2.RegexError as err:
            raise OperatorError(f"operator {op!r}: {err}") from err
    raise OperatorError(f"operator {op!r} is not defined for type string")


def _number_test(op: str, value: Any) -> Callable[[Any], bool]:
    if op in ("eq", "neq", "lt", "lte", "gt", "gte"):
        want = to_number(value)
        if want is None:
            raise OperatorError(f"operator {op!r} needs a number value")
        return _compare(op, lambda x: (x > want) - (x < want))
    if op in ("in", "not_in"):
        if not isinstance(value, (list, tuple)):
            raise OperatorError(f"operator {op!r} needs a list of numbers")
        numbers: set[float] = set()
        for element in value:
            number = to_number(element)
            if number is None:
                raise OperatorError(f"operator {op!r} needs a list of numbers")
            numbers.add(number)
        return (lambda x: x in numbers) if op == "in" else (lambda x: x not in numbers)
    raise OperatorError(f"operator {op!r} is not defined for type number")


def _semver_test(op: str, value: Any) -> Callable[[Any], bool]:
    if op in ("eq", "neq", "lt", "lte", "gt", "gte"):
        if not isinstance(value, str):
            raise OperatorError(f"operator {op!r} needs a string value")
        want = parse_semver(value)
        if want is None:
            # An unparsable version makes the condition false.
            return _never
        return _compare(op, lambda x: x.compare(want))
    if op in ("in", "not_in"):
        strings = to_strings(value)
        if strings is None:
            raise OperatorError(f"operator {op!r} needs a list of strings")
        versions: list[Semver] = []
        for s in strings:
            parsed = parse_semver(s)
            if parsed is None:
                # An unparsable version makes the condition false, as it does
                # for eq and neq: skipping it instead would make
                # not_in ["garbage"] match every version.
                return _never
            versions.append(parsed)
        negate = op == "not_in"
        return lambda x: any(x.compare(v) == 0 for v in versions) != negate
    raise OperatorError(f"operator {op!r} is not defined for type semver")


def _date_test(op: str, value: Any) -> Callable[[Any], bool]:
    if op in ("before", "after"):
        if not isinstance(value, str):
            raise OperatorError(f"operator {op!r} needs an RFC 3339 string")
        want = parse_rfc3339(value)
        if want is None:
            # An unparsable date makes the condition false.
            return _never
        return (lambda x: x < want) if op == "before" else (lambda x: x > want)
    raise OperatorError(f"operator {op!r} is not defined for type date")


def _list_test(op: str, value: Any) -> Callable[[Any], bool]:
    if op in ("contains", "not_contains"):
        if not isinstance(value, str):
            raise OperatorError(f"operator {op!r} needs a string value")
        want = value
        return (lambda x: want in x) if op == "contains" else (lambda x: want not in x)
    if op in ("contains_any", "contains_all"):
        wants = to_strings(value)
        if wants is None:
            raise OperatorError(f"operator {op!r} needs a list of strings")
        if op == "contains_any":
            return lambda x: any(w in x for w in wants)
        return lambda x: all(w in x for w in wants)
    if op == "is_empty":
        return lambda x: len(x) == 0
    raise OperatorError(f"operator {op!r} is not defined for type list")
