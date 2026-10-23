"""A 1,000-flag ruleset shaped like real ones, for the speed guarantee."""

from __future__ import annotations

from typing import Any

from flagnetic import Context

LARGE_FLAGS = 1000


def large_ruleset() -> dict[str, Any]:
    """Attribute conditions on every attribute type, segments, prerequisites,
    percentage rollouts and running experiments, so an evaluation walks the
    paths a production ruleset makes it walk.
    """
    variations = [{"key": "on", "value": True}, {"key": "off", "value": False}]
    segments = [
        {
            "key": f"segment_{s}",
            "private": False,
            "conditions": [
                {"kind": "attribute", "attribute": "country", "op": "in", "value": ["DE", "FR", "NL", "PL"]},
                {"kind": "attribute", "attribute": "age", "op": "gte", "value": 18 + s},
            ],
        }
        for s in range(20)
    ]
    flags = []
    for i in range(LARGE_FLAGS):
        rules: list[dict[str, Any]] = []
        prerequisites: list[dict[str, Any]] = []
        match i % 10:
            case 0:
                rules.append(
                    {
                        "id": f"r{i}",
                        "conditions": [],
                        "serve": {
                            "kind": "experiment",
                            "experiment": f"experiment_{i}",
                            "unit_type": "user",
                            "inclusion_salt": f"in-{i}",
                            "variation_salt": f"var-{i}",
                            "traffic_allocation": 5000,
                            "weights": [
                                {"variation": "on", "weight": 5000},
                                {"variation": "off", "weight": 5000},
                            ],
                        },
                    }
                )
            case 1:
                rules.append(
                    {
                        "id": f"r{i}",
                        "conditions": [],
                        "serve": {
                            "kind": "rollout",
                            "unit_type": "user",
                            "variation": "on",
                            "percentage": 2500,
                            "salt": f"salt-{i}",
                        },
                    }
                )
            case 2:
                prerequisites.append({"flag": f"flag_{i - 1}", "variation": "on"})
            case 3:
                rules.append(
                    {
                        "id": f"r{i}",
                        "conditions": [{"kind": "in_segment", "segment": f"segment_{i % 20}"}],
                        "serve": {"kind": "variation", "variation": "on"},
                    }
                )
        rules.append(
            {
                "id": f"a{i}",
                "conditions": [
                    {"kind": "attribute", "attribute": "plan", "op": "eq", "value": "pro"},
                    {"kind": "attribute", "attribute": "app_version", "op": "gte", "value": f"2.{i % 30}.0"},
                    {"kind": "attribute", "attribute": "tags", "op": "contains", "value": "beta"},
                ],
                "serve": {"kind": "variation", "variation": "on"},
            }
        )
        rules.append(
            {
                "id": f"b{i}",
                "conditions": [
                    {
                        "kind": "attribute",
                        "attribute": "email",
                        "op": "matches",
                        "value": r"@(example|test)\.com$",
                    },
                    {
                        "kind": "attribute",
                        "attribute": "signed_up",
                        "op": "after",
                        "value": "2025-01-01T00:00:00Z",
                    },
                    {"kind": "attribute", "attribute": "beta", "op": "eq", "value": True},
                ],
                "serve": {"kind": "variation", "variation": "on"},
            }
        )
        flags.append(
            {
                "key": f"flag_{i}",
                "type": "boolean",
                "automatic_exposure": False,
                "on": True,
                "variations": variations,
                "off_variation": "off",
                "fallthrough": "off",
                "prerequisites": prerequisites,
                "rules": rules,
            }
        )
    return {
        "schema_version": 1,
        "version": 1,
        "scope": "server",
        "unit_types": ["user"],
        "attributes": [
            {"key": "plan", "type": "string"},
            {"key": "country", "type": "string"},
            {"key": "email", "type": "string"},
            {"key": "age", "type": "number"},
            {"key": "beta", "type": "boolean"},
            {"key": "app_version", "type": "semver"},
            {"key": "signed_up", "type": "date"},
            {"key": "tags", "type": "list"},
        ],
        "segments": segments,
        "flags": flags,
    }


def large_context(i: int) -> Context:
    """The i-th of a varied set of contexts."""
    plans = ["free", "pro", "team"]
    countries = ["DE", "US", "FR", "BR", "NL"]
    return Context(
        units={"user": f"user-{i}"},
        attributes={
            "plan": plans[i % len(plans)],
            "country": countries[i % len(countries)],
            "email": f"person{i}@example.com",
            "age": float(16 + i % 50),
            "beta": i % 2 == 0,
            "app_version": f"2.{i % 40}.{i % 7}",
            "signed_up": "2025-06-01T12:00:00Z",
            "tags": ["early", "beta"][: 1 + i % 2],
        },
    )
