"""Instants as integer nanoseconds since the Unix epoch, so comparison is exact."""

from __future__ import annotations

import re

# The grammar Go's time.Parse(time.RFC3339Nano, s) accepts: its RFC 3339 fast
# path plus what its general layout parser adds when the fast path fails (a
# one-digit hour, a comma before the fraction, an offset of up to 24 hours and
# 60 minutes). Digits past the ninth fraction digit are ignored, as Go
# truncates them. ASCII digits only: \d would match other scripts' digits.
_RFC3339 = re.compile(
    r"([0-9]{4})-([0-9]{2})-([0-9]{2})T([0-9]{1,2}):([0-9]{2}):([0-9]{2})"
    r"(?:[.,]([0-9]+))?(?:(Z)|([+-])([0-9]{2}):([0-9]{2}))",
    re.ASCII,
)

_MAX_EPOCH_MILLIS = 2**53


def parse_rfc3339(s: str) -> int | None:
    """An RFC 3339 timestamp with a Z or numeric offset, exactly as the Go evaluator parses it."""
    m = _RFC3339.fullmatch(s)
    if m is None:
        return None
    year, month, day, hour, minute, second = (int(m[k]) for k in range(1, 7))
    if month < 1 or month > 12 or day < 1 or day > _days_in(year, month):
        return None
    if hour > 23 or minute > 59 or second > 59:
        return None
    offset = 0
    if m[8] is None:
        offset_hours, offset_minutes = int(m[10]), int(m[11])
        if offset_hours > 24 or offset_minutes > 60:
            return None
        offset = (offset_hours * 3600 + offset_minutes * 60) * (-1 if m[9] == "-" else 1)
    nanos = int(m[7][:9].ljust(9, "0")) if m[7] is not None else 0
    seconds = _days_from_civil(year, month, day) * 86400 + hour * 3600 + minute * 60 + second - offset
    return seconds * 1_000_000_000 + nanos


def from_epoch_millis(ms: float) -> int | None:
    """An instant from integral Unix epoch milliseconds within 2^53, the range Go's evaluator accepts."""
    if ms != int(ms) or abs(ms) > _MAX_EPOCH_MILLIS:
        return None
    return int(ms) * 1_000_000


def _days_in(year: int, month: int) -> int:
    if month == 2:
        return 29 if year % 4 == 0 and (year % 100 != 0 or year % 400 == 0) else 28
    return 30 if month in (4, 6, 9, 11) else 31


def _days_from_civil(year: int, month: int, day: int) -> int:
    """Days since 1970-01-01 of a proleptic Gregorian date (Howard Hinnant's days_from_civil)."""
    y = year - 1 if month <= 2 else year
    era = y // 400
    yoe = y - era * 400
    doy = (153 * (month + (-3 if month > 2 else 9)) + 2) // 5 + day - 1
    doe = yoe * 365 + yoe // 4 - yoe // 100 + doy
    return era * 146097 + doe - 719468
