"""The evaluator: decodes a ruleset and evaluates its flags exactly as the reference evaluator does."""

from __future__ import annotations

from .evaluate import evaluate
from .model import Construct, Context, ExperimentDetail, Result
from .ruleset import SCHEMA_VERSION, Ruleset, RulesetError, decode, parse

__all__ = [
    "SCHEMA_VERSION",
    "Construct",
    "Context",
    "ExperimentDetail",
    "Result",
    "Ruleset",
    "RulesetError",
    "decode",
    "evaluate",
    "parse",
]
