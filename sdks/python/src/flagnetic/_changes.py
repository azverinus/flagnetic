"""Which flags a new ruleset may have changed the value of."""

from __future__ import annotations

import json
from collections.abc import Iterable, Mapping
from typing import Any


def _items(document: Mapping[str, Any], member: str) -> list[Mapping[str, Any]]:
    value = document.get(member)
    return [item for item in value if isinstance(item, Mapping)] if isinstance(value, list) else []


def _encode(item: Any) -> str:
    return json.dumps(item, sort_keys=True)


def _differing(before: Mapping[str, str], after: Mapping[str, str]) -> set[str]:
    """The keys whose item was added, removed or changed."""
    return {key for key in before.keys() | after.keys() if before.get(key) != after.get(key)}


def _by_key(document: Mapping[str, Any], member: str) -> dict[str, str]:
    return {str(item.get("key")): _encode(item) for item in _items(document, member)}


def changed_flags(before: Mapping[str, Any] | None, after: Mapping[str, Any]) -> list[str]:
    """The sorted keys of the flags whose evaluation may differ between two ruleset documents.

    Flags added, removed or reconfigured, and flags that reach, through their
    conditions, the conditions of the segments those name, their overrides,
    rollouts and prerequisites, a segment, a declared attribute or unit type, or
    a prerequisite flag that changed, transitively. A flag's own definition
    names its segments and prerequisites but not their contents, so comparing
    definitions alone would miss those.
    """
    old: Mapping[str, Any] = before or {}
    flags = _differing(_by_key(old, "flags"), _by_key(after, "flags"))
    segments = _differing(_by_key(old, "segments"), _by_key(after, "segments"))
    names = _differing(_by_key(old, "attributes"), _by_key(after, "attributes"))
    names |= set(_unit_types(old)) ^ set(_unit_types(after))

    reaches: dict[str, tuple[set[str], set[str], set[str]]] = {}
    for document in (old, after):
        by_segment = {str(s.get("key")): s for s in _items(document, "segments")}
        for flag in _items(document, "flags"):
            key = str(flag.get("key"))
            reach_segments, reach_names, reach_flags = reaches.setdefault(key, (set(), set(), set()))

            def walk(
                conditions: Iterable[Mapping[str, Any]],
                reach_segments: set[str] = reach_segments,
                reach_names: set[str] = reach_names,
                reach_flags: set[str] = reach_flags,
                by_segment: Mapping[str, Mapping[str, Any]] = by_segment,
            ) -> None:
                for condition in conditions:
                    kind = condition.get("kind")
                    if kind in ("in_segment", "not_in_segment"):
                        segment = str(condition.get("segment"))
                        if segment not in reach_segments:
                            reach_segments.add(segment)
                            walk(_items(by_segment.get(segment, {}), "conditions"))
                    elif kind == "prerequisite":
                        reach_flags.add(str(condition.get("flag")))
                    else:
                        # An attribute, or a unit type read as one.
                        reach_names.add(str(condition.get("attribute")))

            reach_flags.update(str(p.get("flag")) for p in _items(flag, "prerequisites"))
            reach_names.update(str(o.get("unit_type")) for o in _items(flag, "overrides"))
            for rule in _items(flag, "rules"):
                walk(_items(rule, "conditions"))
                serve = rule.get("serve")
                if isinstance(serve, Mapping):
                    reach_names.add(str(serve.get("unit_type")))

    # A flag is affected by what it reaches directly, and by any prerequisite
    # affected in turn; repeat until nothing more is.
    grew = True
    while grew:
        grew = False
        for key, (reach_segments, reach_names, reach_flags) in reaches.items():
            if key not in flags and (reach_segments & segments or reach_names & names or reach_flags & flags):
                flags.add(key)
                grew = True
    return sorted(flags)


def _unit_types(document: Mapping[str, Any]) -> list[str]:
    value = document.get("unit_types")
    return [u or "" for u in value] if isinstance(value, list) else []
