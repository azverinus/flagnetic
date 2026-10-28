"""Semantic versions as attribute values and condition values."""

from __future__ import annotations

from functools import lru_cache
from typing import NamedTuple


class Semver(NamedTuple):
    """A parsed version. Build metadata is dropped as it never affects precedence."""

    core: tuple[tuple[int, str], tuple[int, str], tuple[int, str]]
    prerelease: tuple[str, ...]

    def compare(self, other: Semver) -> int:
        """-1, 0 or 1 by SemVer 2.0.0 section 11 precedence."""
        if self.core != other.core:
            return -1 if self.core < other.core else 1
        a, b = self.prerelease, other.prerelease
        if not a and not b:
            return 0
        if not a:
            return 1
        if not b:
            return -1
        for x, y in zip(a, b, strict=False):
            # Identifiers that differ as strings may still compare equal, "01"
            # and "1" say, so the comparison decides, not the strings.
            c = _compare_identifier(x, y)
            if c != 0:
                return c
        if len(a) == len(b):
            return 0
        return -1 if len(a) < len(b) else 1


def _is_numeric(s: str) -> bool:
    return s != "" and all("0" <= c <= "9" for c in s)


def _magnitude(digits: str) -> tuple[int, str]:
    """A sort key for digits of any length: leading zeros dropped, then the length, then the digits."""
    trimmed = digits.lstrip("0") or "0"
    return len(trimmed), trimmed


def _compare_identifier(a: str, b: str) -> int:
    """Numeric identifiers compare numerically and below alphanumeric ones, those in ASCII order."""
    a_num, b_num = _is_numeric(a), _is_numeric(b)
    if a_num and b_num:
        x, y = _magnitude(a), _magnitude(b)
        return (x > y) - (x < y)
    if a_num:
        return -1
    if b_num:
        return 1
    return (a > b) - (a < b)


_IDENTIFIER_CHARS = frozenset("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-")


def _valid_identifiers(s: str) -> bool:
    return all(ident != "" and all(c in _IDENTIFIER_CHARS for c in ident) for ident in s.split("."))


@lru_cache(maxsize=1024)
def parse_semver(s: str) -> Semver | None:
    """Parses an optional lower-case "v", one to three dot-separated integers
    (missing components are 0, so "1.17" equals "1.17.0"), an optional
    pre-release after "-" and an optional build after "+". Numeric components
    are unbounded and leading zeros are ignored. Anything else is not a
    version, and a semver condition on it is false.
    """
    s = s.removeprefix("v")
    if (i := s.find("+")) >= 0:
        if not _valid_identifiers(s[i + 1 :]):
            return None
        s = s[:i]
    prerelease: tuple[str, ...] = ()
    if (i := s.find("-")) >= 0:
        if not _valid_identifiers(s[i + 1 :]):
            return None
        prerelease = tuple(s[i + 1 :].split("."))
        s = s[:i]
    parts = s.split(".")
    if len(parts) > 3 or not all(_is_numeric(p) for p in parts):
        return None
    first, second, third = [_magnitude(p) for p in parts] + [_magnitude("0")] * (3 - len(parts))
    return Semver((first, second, third), prerelease)
