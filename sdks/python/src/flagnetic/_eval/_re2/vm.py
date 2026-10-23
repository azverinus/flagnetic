"""A Pike VM: matches in time linear in the input for a given program."""

from __future__ import annotations

from . import parser
from .program import ASSERT, JMP, LIT, MATCH, SET, SPLIT, Program

_WORD = frozenset("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz")


def search(prog: Program, text: str) -> bool:
    """Reports whether prog matches anywhere in text.

    Every thread advances in lockstep over the code points, a lone surrogate
    reading as U+FFFD as Go reads invalid UTF-8. The first thread to reach the
    end of the program decides, as only whether it matches is asked.
    """
    ops, arg1, arg2, sets = prog
    n = len(text)
    mark = [-1] * len(ops)
    current: list[int] = []
    following: list[int] = []
    stack: list[int] = []

    def add(threads: list[int], pc: int, pos: int, gen: int) -> None:
        stack.append(pc)
        while stack:
            pc = stack.pop()
            if mark[pc] == gen:
                continue
            mark[pc] = gen
            op = ops[pc]
            if op == JMP:
                stack.append(arg1[pc])
            elif op == SPLIT:
                stack.append(arg2[pc])
                stack.append(arg1[pc])
            elif op == ASSERT:
                if _holds(arg1[pc], text, pos, n):
                    stack.append(pc + 1)
            else:
                threads.append(pc)

    gen = 0
    pos = 0
    while True:
        add(current, 0, pos, gen)  # unanchored: a new thread at every position
        cp = -1
        if pos < n:
            cp = ord(text[pos])
            if 0xD800 <= cp <= 0xDFFF:
                cp = 0xFFFD
        for pc in current:
            op = ops[pc]
            if op == MATCH:
                return True
            if cp < 0:
                continue
            if op == LIT:
                if cp == arg1[pc]:
                    add(following, pc + 1, pos + 1, gen + 1)
            elif op == SET:
                cs = sets[pc]
                assert cs is not None
                if cs.ascii[cp] if cp < 128 else cs.has(cp):
                    add(following, pc + 1, pos + 1, gen + 1)
        if pos >= n:
            return False
        pos += 1
        gen += 1
        current, following = following, current
        following.clear()


def _holds(assertion: int, text: str, pos: int, n: int) -> bool:
    if assertion == parser.BEGIN_TEXT:
        return pos == 0
    if assertion == parser.END_TEXT:
        return pos == n
    if assertion == parser.BEGIN_LINE:
        return pos == 0 or text[pos - 1] == "\n"
    if assertion == parser.END_LINE:
        return pos == n or text[pos] == "\n"
    # \b and \B read ASCII word characters, as RE2 does.
    before = pos > 0 and text[pos - 1] in _WORD
    after = pos < n and text[pos] in _WORD
    return (before != after) == (assertion == parser.WORD_BOUNDARY)
