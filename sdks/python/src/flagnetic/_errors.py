"""The base of every exception this package raises on purpose."""

from __future__ import annotations


class FlagneticError(Exception):
    """Base class of the exceptions ``flagnetic`` raises: catch it to handle them all.

    Getters never raise, so these come from the calls that change state, such as
    ``local_overrides.set``. A bad argument is still a ``ValueError``.
    """
