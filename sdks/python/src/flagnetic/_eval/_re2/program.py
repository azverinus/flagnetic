"""A parse tree compiled to the instructions of a Pike VM."""

from __future__ import annotations

from collections.abc import Callable
from typing import NamedTuple

from . import parser
from .charset import CharSet
from .parser import Node

# Opcodes. LIT matches one code point, SET one of a set.
LIT = 0
SET = 1
SPLIT = 2
JMP = 3
ASSERT = 4
MATCH = 5

Work = Node | Callable[[], None]


class Program(NamedTuple):
    """Instructions in parallel lists: opcode, two integer operands, a set.

    LIT: the code point in x. SET: the set. SPLIT: both successors, x the
    preferred. JMP: the target in x. ASSERT: the assertion in x, the successor
    is the next instruction. MATCH: none.
    """

    ops: list[int]
    x: list[int]
    y: list[int]
    sets: list[CharSet | None]


class _Builder:
    def __init__(self) -> None:
        self.program = Program([], [], [], [])
        # Nodes to emit and callbacks that patch jumps once the code they jump
        # over exists; popped from the end.
        self.work: list[Work] = []

    def __len__(self) -> int:
        return len(self.program.ops)

    def emit(self, op: int, x: int = 0, y: int = 0, cs: CharSet | None = None) -> int:
        self.program.ops.append(op)
        self.program.x.append(x)
        self.program.y.append(y)
        self.program.sets.append(cs)
        return len(self) - 1

    def schedule(self, tasks: list[Work]) -> None:
        self.work.extend(reversed(tasks))

    def alternate(self, node: Node) -> None:
        # split(a, L1); a; jmp end; L1: split(b, L2); b; jmp end; L2: c
        exits: list[int] = []
        tasks: list[Work] = []
        last = len(node.subs) - 1
        for k, sub in enumerate(node.subs):
            if k == last:
                tasks.append(sub)
                continue
            fork = [0]

            def start(fork: list[int] = fork) -> None:
                fork[0] = self.emit(SPLIT, len(self) + 1)

            def end(fork: list[int] = fork) -> None:
                exits.append(self.emit(JMP))
                self.program.y[fork[0]] = len(self)

            tasks.extend((start, sub, end))

        def finish() -> None:
            for e in exits:
                self.program.x[e] = len(self)

        tasks.append(finish)
        self.schedule(tasks)

    def repeat(self, node: Node) -> None:
        sub = node.subs[0]
        tasks: list[Work] = [sub] * node.lo
        if node.hi < 0:
            # L: split(body, out); body; jmp L
            loop = [0]

            def open_loop() -> None:
                loop[0] = self.emit(SPLIT, len(self) + 1)

            def close_loop() -> None:
                self.emit(JMP, loop[0])
                self.program.y[loop[0]] = len(self)

            tasks.extend((open_loop, sub, close_loop))
        else:
            # Each optional copy may skip to the end: the language of x{lo,hi}.
            forks: list[int] = []

            def optional() -> None:
                forks.append(self.emit(SPLIT, len(self) + 1))

            for _ in range(node.lo, node.hi):
                tasks.extend((optional, sub))

            def finish() -> None:
                for f in forks:
                    self.program.y[f] = len(self)

            tasks.append(finish)
        self.schedule(tasks)


def compile_tree(root: Node) -> Program:
    """Compiles root to a Program without recursing over the tree's depth."""
    b = _Builder()
    b.work.append(root)
    while b.work:
        item = b.work.pop()
        if not isinstance(item, Node):
            item()
            continue
        kind = item.kind
        if kind == parser.CHAR:
            charset = item.charset
            assert charset is not None
            if len(charset.ranges) == 1 and charset.ranges[0][0] == charset.ranges[0][1]:
                b.emit(LIT, charset.ranges[0][0])
            else:
                b.emit(SET, cs=charset)
        elif kind == parser.ASSERT:
            b.emit(ASSERT, item.what)
        elif kind == parser.CAT:
            b.schedule(list(item.subs))
        elif kind == parser.ALT:
            b.alternate(item)
        else:
            b.repeat(item)
    b.emit(MATCH)
    return b.program
