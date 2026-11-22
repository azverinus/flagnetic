"""The log names every SDK shares are the ones this SDK exports."""

from __future__ import annotations

import re

from flagnetic import _events

from conformance import ROOT

# Operators alert on these names across languages, so they are read from the table every SDK
# follows rather than repeated here.
TABLE = ROOT.parent / "docs" / "sdks" / "conformance.md"

# The names the shared ones replaced: a client logging them would break an alert written against
# the table.
REPLACED = ("sse_connected", "sse_dropped")


def shared_names() -> list[str]:
    """The names in the first column of the table under the "Log names" heading."""
    text = TABLE.read_text(encoding="utf-8")
    section = text.split("## Log names", 1)[1].split("\n## ", 1)[0]
    names = []
    for line in section.splitlines():
        match = re.match(r"\|\s*`([a-z_]+)`\s*\|", line)
        if match:
            names.append(match.group(1))
    return names


def exported() -> set[str]:
    """The string constants the events module names in upper case: the names a client logs."""
    return {value for name, value in vars(_events).items() if name.isupper() and isinstance(value, str)}


def test_the_shared_log_names_are_exported() -> None:
    names = shared_names()
    assert names, "the Log names table holds no names"
    assert sorted(set(names) - exported()) == []


def test_the_replaced_log_names_are_gone() -> None:
    assert sorted(set(REPLACED) & exported()) == []
