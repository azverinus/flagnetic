"""The quickstart of docs/sdks/python.md runs, and the README names the public API exactly."""

from __future__ import annotations

import re
from typing import Any

import pytest

import flagnetic
from mock_server import MockServer, Response
from protocol import SDK_KEY, read

from conformance import ROOT

PAGE = ROOT.parent / "docs" / "sdks" / "python.md"
README = ROOT.parent / "sdks" / "python" / "README.md"


def section(text: str, heading: str) -> str:
    return text.split(f"\n## {heading}\n", 1)[1].split("\n## ", 1)[0]


def test_every_quickstart_snippet_runs(monkeypatch: pytest.MonkeyPatch) -> None:
    server = MockServer(
        lambda _request: Response(
            200, {"Content-Type": "application/json", "ETag": '"1"'}, read("server/ruleset.json")
        )
    )
    try:
        monkeypatch.setenv("FLAGNETIC_SDK_KEY", SDK_KEY)
        monkeypatch.setenv("FLAGNETIC_URL", server.url)
        blocks = re.findall(
            r"```python\n(.*?)```", section(PAGE.read_text(encoding="utf-8"), "Quickstart"), re.S
        )
        assert len(blocks) >= 4
        scope: dict[str, Any] = {}
        for block in blocks:
            exec(compile(block, str(PAGE), "exec"), scope)
        assert scope["stage"] == flagnetic.Stage.FRESH
        assert isinstance(scope["enabled"], bool)
        assert scope["detail"].reason
    finally:
        server.close()


def test_the_readme_names_the_exported_api() -> None:
    text = section(README.read_text(encoding="utf-8"), "What 1.0 promises")
    named = set(re.findall(r"`(flagnetic\.[A-Za-z_.]+|[A-Z][A-Za-z]+)`", text))
    for name in flagnetic.__all__:
        if name != "__version__":
            assert name in {n.removeprefix("flagnetic.") for n in named}, (
                f"{name} is exported but not promised"
            )


def test_the_two_override_errors_share_the_base() -> None:
    assert issubclass(flagnetic.LocalOverridesNotAllowed, flagnetic.FlagneticError)
    assert issubclass(flagnetic.LocalOverrideRefused, flagnetic.FlagneticError)
