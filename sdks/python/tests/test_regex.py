"""The regular expression engine beyond the shared vectors."""

from __future__ import annotations

import time

import pytest

from flagnetic._eval._re2 import RegexError, compile_pattern


def matches(pattern: str, text: str) -> bool:
    return compile_pattern(pattern)(text)


def test_matching_time_is_linear_in_the_input() -> None:
    # A backtracking engine takes exponential time on both of these.
    nested = compile_pattern("(a*)*b")
    alternation = compile_pattern("(a|aa)+$")
    start = time.perf_counter()
    assert not nested("a" * 3000)
    assert not alternation("a" * 3000 + "b")
    assert time.perf_counter() - start < 5


def test_nesting_the_parser_accepts_does_not_exhaust_the_stack() -> None:
    depth = 900
    assert matches("(" * depth + "a" + ")" * depth, "xax")
    assert matches("(?:" * depth + "a" + ")" * depth, "a")
    with pytest.raises(RegexError):
        compile_pattern("(" * 1001 + "a" + ")" * 1001)


def test_a_long_alternation_is_compiled_without_recursion() -> None:
    pattern = "|".join(f"w{i}" for i in range(5000))
    assert matches(pattern, "xw4999y")
    assert not matches(pattern, "x5000")


@pytest.mark.parametrize(
    "pattern", ["a{1001}", "(a{100}){100}", "a{2,1}", "(?=a)", r"\1", r"\C", "a**", "[z-a]", r"\p{Nope}"]
)
def test_what_go_refuses_is_refused(pattern: str) -> None:
    with pytest.raises(RegexError):
        compile_pattern(pattern)


def test_a_repeat_within_the_limit_matches() -> None:
    assert matches("^(ab){500}$", "ab" * 500)
    assert not matches("^(ab){500}$", "ab" * 499)


def test_a_lone_surrogate_in_the_input_reads_as_the_replacement_character() -> None:
    assert matches(r"^\x{FFFD}$", "\ud800")
    assert not matches("^a$", "\ud800")


def test_unicode_classes_use_go_tables() -> None:
    assert matches(r"^\p{Greek}+$", "αβγ")
    assert matches(r"^\p{Lu}$", "É")
    assert not matches(r"^\p{Lu}$", "é")
    assert matches(r"(?i)^\p{Lu}$", "é")
    assert matches(r"^\p{Letter}$", "ж")


def test_case_folding_follows_unicode_simple_folding() -> None:
    assert matches("(?i)k", "K")  # Kelvin sign
    assert matches("(?i)ſ", "S")
    assert matches("(?i)σ", "ς")
    assert not matches("k", "K")


def test_word_boundaries_and_classes_are_ascii() -> None:
    assert not matches(r"^\w$", "é")
    assert matches(r"\bfoo\b", "a foo b")
    assert not matches(r"\bé", "é")
    assert not matches(r"^\d$", "٣")


def test_multiline_anchors() -> None:
    assert matches("(?m)^b$", "a\nb\nc")
    assert not matches("^b$", "a\nb\nc")
    assert not matches("a$", "a\n")
