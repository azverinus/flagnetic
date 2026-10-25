"""The core package without the openfeature extra: the OpenFeature SDK is never needed to use flagnetic."""

from __future__ import annotations

import subprocess
import sys
import textwrap

# Runs in a fresh interpreter in which importing the OpenFeature SDK fails, as it does where the
# extra is not installed, whether or not this environment has it.
_SCRIPT = textwrap.dedent(
    """
    import sys

    sys.modules["openfeature"] = None

    import flagnetic
    from flagnetic import testing

    client = testing.from_values({"checkout": True})
    assert client.get_bool(flagnetic.Context(units={"user": "u-1"}), "checkout", False) is True
    assert "openfeature" not in [name for name, module in sys.modules.items() if module is not None]

    try:
        import flagnetic.openfeature
    except ImportError as err:
        print(err)
    else:
        raise SystemExit("flagnetic.openfeature imported without the OpenFeature SDK")
    """
)


def test_the_core_package_works_and_the_provider_names_the_extra() -> None:
    done = subprocess.run([sys.executable, "-c", _SCRIPT], capture_output=True, text=True, check=False)
    assert done.returncode == 0, done.stderr
    assert "pip install 'flagnetic[openfeature]'" in done.stdout
