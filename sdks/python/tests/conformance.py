"""Where the shared conformance suite lives and how to read it."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[3] / "conformance"
FIXTURES = ROOT / "fixtures"


def load(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def same_json(a: Any, b: Any) -> bool:
    """Whether two decoded JSON values are equal: 5 equals 5.0, true is not 1."""
    if isinstance(a, bool) or isinstance(b, bool):
        return isinstance(a, bool) and isinstance(b, bool) and a == b
    if isinstance(a, (int, float)) and isinstance(b, (int, float)):
        return a == b
    if isinstance(a, dict) and isinstance(b, dict):
        return a.keys() == b.keys() and all(same_json(a[k], b[k]) for k in a)
    if isinstance(a, list) and isinstance(b, list):
        return len(a) == len(b) and all(same_json(x, y) for x, y in zip(a, b, strict=True))
    return type(a) is type(b) and a == b
