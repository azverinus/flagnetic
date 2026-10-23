"""Checks the wheel and the sdist `uv build` made: what a consumer installs.

python scripts/check_dist.py dist
"""

from __future__ import annotations

import sys
import tarfile
import zipfile
from email.parser import Parser
from pathlib import Path

WHEEL_FILES = ("flagnetic/__init__.py", "flagnetic/py.typed", "flagnetic/_eval/_re2/unicode_tables.py")
SDIST_FILES = (
    "pyproject.toml",
    "README.md",
    "LICENSE",
    "src/flagnetic/py.typed",
    "tests/test_conformance.py",
)


def fail(message: str) -> None:
    sys.stderr.write(f"check_dist: {message}\n")
    raise SystemExit(1)


def check_wheel(path: Path) -> None:
    if not path.name.endswith("-py3-none-any.whl"):
        fail(f"{path.name} is not a pure Python wheel")
    with zipfile.ZipFile(path) as wheel:
        names = set(wheel.namelist())
        for expected in WHEEL_FILES:
            if expected not in names:
                fail(f"{path.name} lacks {expected}")
        (metadata_name,) = (n for n in names if n.endswith(".dist-info/METADATA"))
        metadata = Parser().parsestr(wheel.read(metadata_name).decode("utf-8"))
    if metadata["Name"] != "flagnetic":
        fail(f"the wheel is named {metadata['Name']}")
    if metadata["Requires-Python"] != ">=3.11":
        fail(f"Requires-Python is {metadata['Requires-Python']}, want >=3.11")
    # The core package installs nothing; a dependency belongs to an extra.
    for requirement in metadata.get_all("Requires-Dist") or []:
        if "extra ==" not in requirement:
            fail(f"the wheel requires {requirement} outside an extra")
    if "Typing :: Typed" not in (metadata.get_all("Classifier") or []):
        fail("the wheel is not classified Typing :: Typed")


def check_sdist(path: Path) -> None:
    with tarfile.open(path) as sdist:
        names = {n.split("/", 1)[1] for n in sdist.getnames() if "/" in n}
    for expected in SDIST_FILES:
        if expected not in names:
            fail(f"{path.name} lacks {expected}")


def main() -> None:
    dist = Path(sys.argv[1] if len(sys.argv) > 1 else "dist")
    wheels = sorted(dist.glob("flagnetic-*.whl"))
    sdists = sorted(dist.glob("flagnetic-*.tar.gz"))
    if len(wheels) != 1 or len(sdists) != 1:
        fail(f"want one wheel and one sdist in {dist}, found {[p.name for p in (*wheels, *sdists)]}")
    check_wheel(wheels[0])
    check_sdist(sdists[0])
    sys.stdout.write(f"{wheels[0].name} and {sdists[0].name} are well formed\n")


if __name__ == "__main__":
    main()
