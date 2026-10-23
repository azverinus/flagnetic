"""The `matches` operator: RE2 syntax with RE2 semantics, in linear time.

A pattern is parsed exactly as Go's regexp/syntax parses it (Perl flags, as
regexp.Compile uses) and matched by a Pike VM, so a pattern behaves as it does
in the Go evaluator and no input can make matching backtrack exponentially:
Python's own `re` is never run over a context value. Case folding and Unicode
classes use Go's tables (see unicode_tables), not the interpreter's.
"""

from __future__ import annotations

from collections.abc import Callable

from .parser import RegexError, parse
from .program import compile_tree
from .vm import search

Matcher = Callable[[str], bool]


def compile_pattern(pattern: str) -> Matcher:
    """Compiles an RE2 pattern into a matcher, or raises RegexError."""
    program = compile_tree(parse(pattern))
    return lambda text: search(program, text)


__all__ = ["Matcher", "RegexError", "compile_pattern"]
