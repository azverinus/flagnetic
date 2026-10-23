"""The evaluator: decodes a ruleset and evaluates its flags exactly as the reference evaluator does."""

from __future__ import annotations

from .evaluate import evaluate
from .model import Construct, Context, ExperimentDetail, Result
from .ruleset import Ruleset, RulesetError, decode

__all__ = [
    "Construct",
    "Context",
    "ExperimentDetail",
    "Result",
    "Ruleset",
    "RulesetError",
    "decode",
    "evaluate",
]
