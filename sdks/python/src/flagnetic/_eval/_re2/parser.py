"""A parser for RE2 syntax, as Go's regexp/syntax parses it with Perl flags.

Every syntax error Go reports is a RegexError here: look-around, back
references, ``\\C``, nested repetition operators, repeat counts above 1000,
unbalanced groups and brackets and unknown class names. Nothing recurses over
the pattern's nesting, so a deeply nested pattern cannot exhaust the stack.
"""

from __future__ import annotations

from typing import NamedTuple

from . import charset
from .charset import CharSet, Ranges

MAX_REPEAT = 1000
MAX_DEPTH = 1000
# Go's limit on the size of a compiled expression, in instructions.
MAX_SIZE = (128 << 20) // 40

# Node kinds.
CHAR = 0
ASSERT = 1
CAT = 2
ALT = 3
REPEAT = 4

# Assertions.
BEGIN_TEXT = 0
END_TEXT = 1
BEGIN_LINE = 2
END_LINE = 3
WORD_BOUNDARY = 4
NOT_WORD_BOUNDARY = 5


class RegexError(ValueError):
    """The pattern is not RE2 syntax."""


class Flags(NamedTuple):
    fold_case: bool
    multi_line: bool
    dot_nl: bool


class Node:
    """A node of the parse tree.

    ``need`` is the smallest repeat budget Go's repeatIsValid accepts for the
    subtree, ``size`` its approximate length in instructions.
    """

    __slots__ = ("charset", "counted", "hi", "kind", "lo", "need", "size", "subs", "what")

    def __init__(
        self,
        kind: int,
        *,
        charset: CharSet | None = None,
        what: int = 0,
        subs: list[Node] | None = None,
        lo: int = 0,
        hi: int = 0,
        counted: bool = False,
    ) -> None:
        self.kind = kind
        self.charset = charset
        self.what = what
        self.subs = subs if subs is not None else []
        self.lo = lo
        self.hi = hi
        self.counted = counted
        self.need = 0
        self.size = 1
        if kind == CAT:
            self.need = max((s.need for s in self.subs), default=0)
            self.size = sum(s.size for s in self.subs)
        elif kind == ALT:
            self.need = max((s.need for s in self.subs), default=0)
            self.size = sum(s.size for s in self.subs) + len(self.subs) - 1
        elif kind == REPEAT:
            inner = self.subs[0]
            if not counted:
                self.need = inner.need
                self.size = inner.size + 2
            else:
                bound = hi if hi >= 0 else lo
                self.need = 0 if bound == 0 else bound * max(inner.need, 1)
                copies = hi if hi >= 0 else lo + 1
                self.size = inner.size * max(copies, 1) + 3


def _cat(items: list[Node]) -> Node:
    return items[0] if len(items) == 1 else Node(CAT, subs=items)


def _alt(subs: list[Node]) -> Node:
    return subs[0] if len(subs) == 1 else Node(ALT, subs=subs)


def _char(ranges: Ranges) -> Node:
    return Node(CHAR, charset=CharSet(ranges))


_NO_NEWLINE: Ranges = [(0, 9), (0xB, charset.MAX_RUNE)]
_ANYTHING: Ranges = [(0, charset.MAX_RUNE)]


class _Frame:
    __slots__ = ("alternatives", "flags", "items")

    def __init__(self, flags: Flags) -> None:
        self.flags = flags
        self.alternatives: list[Node] = []
        self.items: list[Node] = []


def parse(pattern: str) -> Node:
    """Parses pattern, or raises RegexError."""
    return _Parser(pattern).parse()


def _is_digit(c: int) -> bool:
    return 0x30 <= c <= 0x39


def _is_octal(c: int) -> bool:
    return 0x30 <= c <= 0x37


def _is_alnum(c: int) -> bool:
    return 0x30 <= c <= 0x39 or 0x41 <= c <= 0x5A or 0x61 <= c <= 0x7A


def _unhex(c: int) -> int:
    if 0x30 <= c <= 0x39:
        return c - 0x30
    if 0x41 <= c <= 0x46:
        return c - 0x41 + 10
    if 0x61 <= c <= 0x66:
        return c - 0x61 + 10
    return -1


class _Parser:
    def __init__(self, pattern: str) -> None:
        # A lone surrogate reads as U+FFFD, as Go reads invalid UTF-8.
        self.p = [0xFFFD if 0xD800 <= ord(ch) <= 0xDFFF else ord(ch) for ch in pattern]
        self.i = 0

    def at(self, k: int) -> int:
        """The code point k places ahead, or -1 past the end."""
        j = self.i + k
        return self.p[j] if 0 <= j < len(self.p) else -1

    def parse(self) -> Node:
        p = self.p
        stack = [_Frame(Flags(False, False, False))]
        last_repeat = False
        while self.i < len(p):
            frame = stack[-1]
            c = p[self.i]
            repeat = False
            if c == 0x28:  # (
                if self.at(1) == 0x3F:
                    opens, flags = self.perl_flags(frame.flags)
                    if opens:
                        stack.append(_Frame(flags))
                    else:
                        frame.flags = flags
                else:
                    self.i += 1
                    stack.append(_Frame(frame.flags))
                if len(stack) > MAX_DEPTH:
                    raise RegexError("expression nests too deeply")
            elif c == 0x7C:  # |
                self.i += 1
                frame.alternatives.append(_cat(frame.items))
                frame.items = []
            elif c == 0x29:  # )
                self.i += 1
                if len(stack) == 1:
                    raise RegexError("unexpected )")
                stack.pop()
                stack[-1].items.append(_alt([*frame.alternatives, _cat(frame.items)]))
            elif c == 0x5E:  # ^
                self.i += 1
                frame.items.append(Node(ASSERT, what=BEGIN_LINE if frame.flags.multi_line else BEGIN_TEXT))
            elif c == 0x24:  # $
                self.i += 1
                frame.items.append(Node(ASSERT, what=END_LINE if frame.flags.multi_line else END_TEXT))
            elif c == 0x2E:  # .
                self.i += 1
                frame.items.append(_char(_ANYTHING if frame.flags.dot_nl else _NO_NEWLINE))
            elif c == 0x5B:  # [
                frame.items.append(self.char_class(frame.flags))
            elif c in (0x2A, 0x2B, 0x3F):  # * + ?
                self.i += 1
                self.repeat(frame, 1 if c == 0x2B else 0, 1 if c == 0x3F else -1, False, last_repeat)
                repeat = True
            elif c == 0x7B:  # {
                counts = self.repeat_counts()
                if counts is None:
                    self.i += 1
                    frame.items.append(self.literal(c, frame.flags))
                else:
                    lo, hi = counts
                    if lo < 0 or lo > MAX_REPEAT or hi > MAX_REPEAT or (hi >= 0 and lo > hi):
                        raise RegexError("invalid repeat count")
                    self.repeat(frame, lo, hi, True, last_repeat)
                    repeat = True
            elif c == 0x5C:  # \
                self.escape(frame)
            else:
                self.i += 1
                frame.items.append(self.literal(c, frame.flags))
            last_repeat = repeat
        if len(stack) != 1:
            raise RegexError("missing )")
        root = stack[0]
        node = _alt([*root.alternatives, _cat(root.items)])
        if node.size > MAX_SIZE:
            raise RegexError("expression too large")
        return node

    def literal(self, cp: int, flags: Flags) -> Node:
        if flags.fold_case:
            return _char(charset.fold([(cp, cp)]))
        return _char([(cp, cp)])

    def repeat(self, frame: _Frame, lo: int, hi: int, counted: bool, last_repeat: bool) -> None:
        # A lazy ? is consumed with the operator: the language is the same.
        if self.at(0) == 0x3F:
            self.i += 1
        if last_repeat:
            raise RegexError("invalid nested repetition operator")
        if not frame.items:
            raise RegexError("missing argument to repetition operator")
        sub = frame.items.pop()
        node = Node(REPEAT, subs=[sub], lo=lo, hi=hi, counted=counted)
        if counted and (lo >= 2 or hi >= 2) and node.need > MAX_REPEAT:
            raise RegexError("invalid repeat count")
        if node.size > MAX_SIZE:
            raise RegexError("expression too large")
        frame.items.append(node)

    def repeat_counts(self) -> tuple[int, int] | None:
        """Parses {n}, {n,} or {n,m} at the cursor, as Go's parseRepeat.

        Anything else leaves the { a literal. A count too big reads as -1.
        """
        j = self.i + 1
        p = self.p

        def digit(k: int) -> bool:
            return k < len(p) and _is_digit(p[k])

        def integer() -> int | None:
            nonlocal j
            if not digit(j):
                return None
            if p[j] == 0x30 and digit(j + 1):  # no leading zeros
                return None
            n = 0
            while digit(j):
                if n >= 100_000_000:
                    n = -1
                elif n >= 0:
                    n = n * 10 + p[j] - 0x30
                j += 1
            return n

        lo = integer()
        if lo is None:
            return None
        hi = lo
        if j < len(p) and p[j] == 0x2C:
            j += 1
            if j < len(p) and p[j] == 0x7D:
                hi = -1
            else:
                m = integer()
                if m is None:
                    return None
                if m < 0:
                    lo = -1
                hi = m
        if j >= len(p) or p[j] != 0x7D:
            return None
        self.i = j + 1
        return lo, hi

    def perl_flags(self, current: Flags) -> tuple[bool, Flags]:
        """Parses what follows "(?": a named group, or a flag change or group."""
        p = self.p
        starts_with_p = self.at(2) == 0x50 and self.at(3) == 0x3C and self.i + 4 < len(p)
        starts_with_name = self.at(2) == 0x3C and self.i + 3 < len(p)
        if starts_with_p or starts_with_name:
            start = self.i + (4 if starts_with_p else 3)
            end = p.index(0x3E, self.i) if 0x3E in p[self.i :] else -1
            if end < 0:
                raise RegexError("invalid named capture")
            name = p[start:end]
            if not name or not all(cp == 0x5F or _is_alnum(cp) for cp in name):
                raise RegexError("invalid named capture")
            self.i = end + 1
            return True, current
        self.i += 2
        fold_case, multi_line, dot_nl = current
        negate = False
        saw_flag = False
        while self.i < len(p):
            c = p[self.i]
            self.i += 1
            if c == 0x69:  # i
                fold_case, saw_flag = not negate, True
            elif c == 0x6D:  # m
                multi_line, saw_flag = not negate, True
            elif c == 0x73:  # s
                dot_nl, saw_flag = not negate, True
            elif c == 0x55:  # U: non-greedy by default; the same language
                saw_flag = True
            elif c == 0x2D:  # -
                if negate:
                    raise RegexError("invalid or unsupported Perl syntax")
                negate, saw_flag = True, False
            elif c in (0x3A, 0x29):  # : )
                if negate and not saw_flag:
                    raise RegexError("invalid or unsupported Perl syntax")
                return c == 0x3A, Flags(fold_case, multi_line, dot_nl)
            else:
                raise RegexError("invalid or unsupported Perl syntax")
        raise RegexError("invalid or unsupported Perl syntax")

    def escape(self, frame: _Frame) -> None:
        nxt = self.at(1)
        if nxt == 0x41:  # \A
            self.i += 2
            frame.items.append(Node(ASSERT, what=BEGIN_TEXT))
        elif nxt == 0x7A:  # \z
            self.i += 2
            frame.items.append(Node(ASSERT, what=END_TEXT))
        elif nxt == 0x62:  # \b
            self.i += 2
            frame.items.append(Node(ASSERT, what=WORD_BOUNDARY))
        elif nxt == 0x42:  # \B
            self.i += 2
            frame.items.append(Node(ASSERT, what=NOT_WORD_BOUNDARY))
        elif nxt == 0x43:  # \C
            raise RegexError("invalid escape sequence: \\C")
        elif nxt == 0x51:  # \Q...\E: literals up to \E or the end
            self.i += 2
            while self.i < len(self.p):
                if self.p[self.i] == 0x5C and self.at(1) == 0x45:
                    self.i += 2
                    return
                frame.items.append(self.literal(self.p[self.i], frame.flags))
                self.i += 1
        elif nxt in (0x70, 0x50):  # \p \P
            frame.items.append(_char(self.unicode_class(frame.flags.fold_case)))
        elif (group := self.perl_class_escape()) is not None:
            negated, ranges = group
            frame.items.append(_char(charset.group_ranges(negated, ranges, frame.flags.fold_case)))
        else:
            frame.items.append(self.literal(self.single_escape(), frame.flags))

    def perl_class_escape(self) -> tuple[bool, Ranges] | None:
        """Consumes \\d \\D \\s \\S \\w \\W."""
        if self.at(0) != 0x5C or self.at(1) < 0:
            return None
        group = charset.PERL_GROUPS.get(chr(self.at(1)))
        if group is not None:
            self.i += 2
        return group

    def single_escape(self) -> int:
        """Consumes a one-character escape, as Go's parseEscape."""
        p = self.p
        self.i += 1  # backslash
        if self.i >= len(p):
            raise RegexError("trailing backslash at end of expression")
        c = p[self.i]
        self.i += 1
        if c < 0x80 and not _is_alnum(c):
            return c
        if 0x31 <= c <= 0x37 and not (self.at(0) >= 0 and _is_octal(self.at(0))):
            raise RegexError("invalid escape sequence: back-references are not supported")
        if 0x30 <= c <= 0x37:
            r = c - 0x30
            for _ in range(2):
                if self.at(0) >= 0 and _is_octal(self.at(0)):
                    r = r * 8 + p[self.i] - 0x30
                    self.i += 1
            return r
        if c == 0x78:  # x
            if self.at(0) == 0x7B:
                self.i += 1
                r = 0
                digits = 0
                while True:
                    if self.i >= len(p):
                        raise RegexError("invalid escape sequence")
                    d = p[self.i]
                    self.i += 1
                    if d == 0x7D:
                        break
                    v = _unhex(d)
                    if v < 0:
                        raise RegexError("invalid escape sequence")
                    r = r * 16 + v
                    if r > charset.MAX_RUNE:
                        raise RegexError("invalid escape sequence")
                    digits += 1
                if digits == 0:
                    raise RegexError("invalid escape sequence")
                return r
            x, y = _unhex(self.at(0)), _unhex(self.at(1))
            if x < 0 or y < 0:
                raise RegexError("invalid escape sequence")
            self.i += 2
            return x * 16 + y
        simple = {0x61: 0x07, 0x66: 0x0C, 0x6E: 0x0A, 0x72: 0x0D, 0x74: 0x09, 0x76: 0x0B}
        if c in simple:
            return simple[c]
        raise RegexError("invalid escape sequence")

    def char_class(self, flags: Flags) -> Node:
        """Parses [...] as Go's parseClass.

        A leading ] is literal, - is literal where it cannot form a range,
        [:name:] is a POSIX class. Under case folding every member is closed
        under folding before the class is negated.
        """
        p = self.p
        self.i += 1  # [
        negated = self.at(0) == 0x5E
        if negated:
            self.i += 1
        ranges: Ranges = []
        first = True
        while True:
            if self.i >= len(p):
                raise RegexError("missing closing ]")
            if p[self.i] == 0x5D and not first:
                break
            first = False

            if p[self.i] == 0x5B and self.at(1) == 0x3A:
                end = self.index_of_pair(0x3A, 0x5D, self.i + 2)
                if end >= 0:
                    name = "".join(chr(cp) for cp in p[self.i + 2 : end])
                    group_negated = name.startswith("^")
                    members = charset.POSIX_GROUPS.get(name[1:] if group_negated else name)
                    if members is None:
                        raise RegexError(f"invalid character class range: [:{name}:]")
                    ranges.extend(charset.group_ranges(group_negated, members, flags.fold_case))
                    self.i = end + 2
                    continue
            if p[self.i] == 0x5C and self.at(1) in (0x70, 0x50):
                ranges.extend(self.unicode_class(flags.fold_case))
                continue
            group = self.perl_class_escape()
            if group is not None:
                ranges.extend(charset.group_ranges(group[0], group[1], flags.fold_case))
                continue
            lo = self.class_char()
            hi = lo
            if self.at(0) == 0x2D and self.i + 1 < len(p) and p[self.i + 1] != 0x5D:
                self.i += 1
                hi = self.class_char()
                if hi < lo:
                    raise RegexError("invalid character class range")
            ranges.extend(charset.fold([(lo, hi)]) if flags.fold_case else [(lo, hi)])
        self.i += 1  # ]
        merged = charset.normalize(ranges)
        return _char(charset.negate(merged) if negated else merged)

    def index_of_pair(self, a: int, b: int, start: int) -> int:
        p = self.p
        for k in range(start, len(p) - 1):
            if p[k] == a and p[k + 1] == b:
                return k
        return -1

    def unicode_class(self, fold_case: bool) -> Ranges:
        """Parses \\pN, \\p{Name}, \\P{...} and \\p{^...} as Go's parseUnicodeClass."""
        p = self.p
        negated = self.at(1) == 0x50
        if self.at(2) != 0x7B:
            # A single-letter name; none when the pattern ends.
            name = [p[self.i + 2]] if self.i + 2 < len(p) else []
            self.i += 2 + len(name)
        else:
            end = p.index(0x7D, self.i) if 0x7D in p[self.i :] else -1
            if end < 0:
                raise RegexError("invalid character class range")
            name = p[self.i + 3 : end]
            self.i = end + 1
        if name and name[0] == 0x5E:
            negated = not negated
            name = name[1:]
        text = "".join(chr(cp) for cp in name)
        ranges = charset.unicode_class(text, negated, fold_case)
        if ranges is None:
            raise RegexError(f"invalid character class range: \\p{{{text}}}")
        return ranges

    def class_char(self) -> int:
        if self.i >= len(self.p):
            raise RegexError("missing closing ]")
        if self.p[self.i] == 0x5C:
            return self.single_escape()
        cp = self.p[self.i]
        self.i += 1
        return cp
