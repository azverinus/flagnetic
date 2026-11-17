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

# The cost of a match is the input times the program, and the input is a context value a caller
# chose. Past this many UTF-8 bytes the value is a non-match instead of a long stall of the
# evaluating thread.
MAX_INPUT_BYTES = 64 << 10


def compile_pattern(pattern: str) -> Matcher:
    """Compiles an RE2 pattern into a matcher, or raises RegexError."""
    program = compile_tree(parse(pattern))

    def match(text: str) -> bool:
        # A code point is at most four bytes (a lone surrogate counts as three), so most
        # inputs are decided by their length without encoding them.
        if len(text) > MAX_INPUT_BYTES:
            return False
        if len(text) * 4 > MAX_INPUT_BYTES and len(text.encode("utf-8", "surrogatepass")) > MAX_INPUT_BYTES:
            return False
        return search(program, text)

    return match


__all__ = ["Matcher", "RegexError", "compile_pattern"]
