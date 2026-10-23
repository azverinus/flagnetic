"""Sets of code points: sorted ranges, case folding closure and Unicode classes."""

from __future__ import annotations

from bisect import bisect_left, bisect_right
from functools import cache

from . import unicode_tables as tables

MAX_RUNE = 0x10FFFF

Ranges = list[tuple[int, int]]


class CharSet:
    """A set of code points, immutable once built, with a direct table for ASCII."""

    __slots__ = ("ascii", "his", "los", "ranges")

    def __init__(self, ranges: Ranges) -> None:
        self.ranges = ranges
        self.los = [lo for lo, _ in ranges]
        self.his = [hi for _, hi in ranges]
        ascii_table = bytearray(128)
        for lo, hi in ranges:
            if lo > 127:
                break
            for cp in range(lo, min(hi, 127) + 1):
                ascii_table[cp] = 1
        self.ascii = bytes(ascii_table)

    def has(self, cp: int) -> bool:
        i = bisect_right(self.los, cp) - 1
        return i >= 0 and cp <= self.his[i]


def normalize(ranges: Ranges) -> Ranges:
    """Sorts ranges and merges those that overlap or touch."""
    out: Ranges = []
    for lo, hi in sorted(ranges):
        if out and lo <= out[-1][1] + 1:
            if hi > out[-1][1]:
                out[-1] = (out[-1][0], hi)
        else:
            out.append((lo, hi))
    return out


def negate(ranges: Ranges) -> Ranges:
    """The complement of ranges within all code points."""
    out: Ranges = []
    nxt = 0
    for lo, hi in normalize(ranges):
        if lo > nxt:
            out.append((nxt, lo - 1))
        nxt = max(nxt, hi + 1)
    if nxt <= MAX_RUNE:
        out.append((nxt, MAX_RUNE))
    return out


@cache
def _fold_table() -> tuple[list[int], dict[int, int]]:
    following: dict[int, int] = {}
    for pair in tables.SIMPLE_FOLD.split():
        cp, nxt = pair.split("-")
        following[int(cp, 16)] = int(nxt, 16)
    return sorted(following), following


def orbit(cp: int) -> list[int]:
    """The code points that fold to each other with cp, itself first."""
    _, following = _fold_table()
    if cp not in following:
        return [cp]
    out = [cp]
    nxt = following[cp]
    while nxt != cp:
        out.append(nxt)
        nxt = following[nxt]
    return out


def fold(ranges: Ranges) -> Ranges:
    """ranges closed under simple case folding, as Go's appendFoldedRange."""
    keys, following = _fold_table()
    out = list(ranges)
    for lo, hi in ranges:
        i = bisect_left(keys, lo)
        while i < len(keys) and keys[i] <= hi:
            cp = keys[i]
            nxt = following[cp]
            while nxt != cp:
                out.append((nxt, nxt))
                nxt = following[nxt]
            i += 1
    return normalize(out)


def _decode(text: str) -> Ranges:
    out: Ranges = []
    for pair in text.split():
        lo, hi = pair.split("-")
        out.append((int(lo, 16), int(hi, 16)))
    return out


def canonical_name(name: str) -> str:
    """Go's canonical class name: separators dropped, first letter upper, the rest lower."""
    out: list[str] = []
    first = True
    for c in name:
        if c in "_- ":
            continue
        if first:
            out.append(chr(ord(c) - 32) if "a" <= c <= "z" else c)
            first = False
        else:
            out.append(chr(ord(c) + 32) if "A" <= c <= "Z" else c)
    return "".join(out)


@cache
def _aliases() -> dict[str, str]:
    return {canonical_name(alias): actual for alias, actual in tables.CATEGORY_ALIASES.items()}


def _lookup(name: str) -> tuple[Ranges, Ranges, int] | None:
    """Go's unicodeTable: the table, the characters folding into it, and its sign."""
    if name == "Any":
        everything = [(0, MAX_RUNE)]
        return everything, everything, 1
    if name == "Assigned":
        unassigned = _decode(tables.CATEGORIES["Cn"])
        return unassigned, unassigned, -1
    if name == "Ascii":
        return [(0, 0x7F)], [(0x17F, 0x17F), (0x212A, 0x212A)], 1
    if name == "Lc":
        name = "LC"
    for table, folds in (
        (tables.CATEGORIES, tables.FOLD_CATEGORIES),
        (tables.SCRIPTS, tables.FOLD_SCRIPTS),
    ):
        if name in table:
            return _decode(table[name]), _decode(folds[name]) if name in folds else [], 1
    actual = _aliases().get(name)
    if actual is not None:
        return (
            _decode(tables.CATEGORIES[actual]),
            _decode(tables.FOLD_CATEGORIES[actual]) if actual in tables.FOLD_CATEGORIES else [],
            1,
        )
    return None


def unicode_class(name: str, negated: bool, fold_case: bool) -> Ranges | None:
    """The ranges of \\p{name} (\\P when negated), or None for a name Go rejects."""
    found = _lookup(canonical_name(name))
    if found is None:
        return None
    table, folded, sign = found
    if sign < 0:
        negated = not negated
    ranges = normalize(table + folded) if fold_case and folded else table
    return negate(ranges) if negated else list(ranges)


_DIGIT: Ranges = [(0x30, 0x39)]
_SPACE: Ranges = [(0x09, 0x0A), (0x0C, 0x0D), (0x20, 0x20)]
_WORD: Ranges = [(0x30, 0x39), (0x41, 0x5A), (0x5F, 0x5F), (0x61, 0x7A)]

PERL_GROUPS: dict[str, tuple[bool, Ranges]] = {
    "d": (False, _DIGIT),
    "D": (True, _DIGIT),
    "s": (False, _SPACE),
    "S": (True, _SPACE),
    "w": (False, _WORD),
    "W": (True, _WORD),
}

POSIX_GROUPS: dict[str, Ranges] = {
    "alnum": [(0x30, 0x39), (0x41, 0x5A), (0x61, 0x7A)],
    "alpha": [(0x41, 0x5A), (0x61, 0x7A)],
    "ascii": [(0x00, 0x7F)],
    "blank": [(0x09, 0x09), (0x20, 0x20)],
    "cntrl": [(0x00, 0x1F), (0x7F, 0x7F)],
    "digit": _DIGIT,
    "graph": [(0x21, 0x7E)],
    "lower": [(0x61, 0x7A)],
    "print": [(0x20, 0x7E)],
    "punct": [(0x21, 0x2F), (0x3A, 0x40), (0x5B, 0x60), (0x7B, 0x7E)],
    "space": [(0x09, 0x0D), (0x20, 0x20)],
    "upper": [(0x41, 0x5A)],
    "word": _WORD,
    "xdigit": [(0x30, 0x39), (0x41, 0x46), (0x61, 0x66)],
}


def group_ranges(negated: bool, ranges: Ranges, fold_case: bool) -> Ranges:
    """Go's appendGroup: under case folding a group is closed before it is negated."""
    closed = fold(ranges) if fold_case else list(ranges)
    return negate(closed) if negated else closed
