"""What the package says it is: its version, and the schema range and SDK API path it promises."""

from __future__ import annotations

import importlib.metadata
import os
from pathlib import Path

import pytest

import flagnetic
from flagnetic._delivery import API_PATH
from flagnetic._eval import SCHEMA_VERSION

ROOT = Path(__file__).resolve().parents[1]


def test_the_installed_version_is_the_version_module() -> None:
    # The package's metadata reads its version from the module release-please
    # rewrites, so the version pip reports is the one the User-Agent and the
    # inventory show.
    assert importlib.metadata.version("flagnetic") == flagnetic.__version__


# The release job sets FLAGNETIC_RELEASE_VERSION to the version of the tag it
# publishes, so a package whose version is not the tag's is never published.
@pytest.mark.skipif("FLAGNETIC_RELEASE_VERSION" not in os.environ, reason="not a release run")
def test_the_version_is_the_release_tags() -> None:
    assert flagnetic.__version__ == os.environ["FLAGNETIC_RELEASE_VERSION"]


# docs/sdks/compatibility.md promises, per SDK line, the schema range and the
# SDK API path; its last Python row is this build's line, so a change to
# either needs a row there.
def test_the_compatibility_tables_last_python_row_is_this_builds_schema_range_and_sdk_api_path() -> None:
    doc = (ROOT.parents[1] / "docs" / "sdks" / "compatibility.md").read_text(encoding="utf-8")
    rows = [line for line in doc.splitlines() if line.startswith("| Python |")]
    assert rows, "docs/sdks/compatibility.md has no Python row"
    _, _, _, schema, path, *_ = (cell.strip() for cell in rows[-1].split("|"))
    # This SDK reads one schema version, written "1" in the table.
    assert schema == str(SCHEMA_VERSION)
    assert path == f"`{API_PATH}`"
