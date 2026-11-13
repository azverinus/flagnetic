# frozen_string_literal: true

require "test_helper"

# RegexTest pins the readings of Ruby's own engine that the translation to
# RE2 semantics must not let through. The shared RE2 vectors, with the
# result of Go's regexp, run in ConformanceTest.
class RegexTest < Minitest::Test
  include RulesetHelper

  # Ruby's own engine would read these differently; each must keep RE2's
  # meaning.
  def test_engine_specific_readings_are_not_ruby_ones
    {
      ["^ios$", "evil\nios"] => false, # ^ and $ are not line anchors
      ["(?m)^ios$", "evil\nios"] => true, # unless (?m) says so
      ["(?i)ß", "ss"] => false, # no multi-character case folding
      ["\\s", "\v"] => false, # \s is [\t\n\f\r ]
      ["[a&&b]", "&"] => true, # && is not class intersection
      ["a{,3}", "a{,3}"] => true, # {,n} is a literal
      ["(?=a)", "a"] => "invalid", # no lookarounds
      ["(a)\\1", "aa"] => "invalid", # no backreferences
      ["\\h", "a"] => "invalid" # no Onigmo escapes
    }.each do |(pattern, input), want|
      assert_equal want, matches(pattern, input), "pattern #{pattern.inspect} on #{input.inspect}"
    end
  end

  # Each invalid byte is one U+FFFD, as Go decodes it, not one per
  # malformed sequence.
  def test_invalid_utf8_input_reads_as_replacement_characters
    assert matches("^a\uFFFDb$", "a\xFFb".b)
    assert matches("^a..b$", "a\xE2\x82b".b)
  end

  # Nested bounded repeats make a backtracking engine burn CPU on input an
  # end user chooses; the answer must be RE2's, and quick, not a cut-off.
  def test_nested_repeats_answer_as_re2_does_without_a_time_limit
    pattern = "^(?:(?:a{1,60}){1,16}X|a+Y)$"
    started = Process.clock_gettime(Process::CLOCK_MONOTONIC)

    assert_equal true, matches(pattern, "#{"a" * 50}Y")
    assert_equal false, matches(pattern, "#{"a" * 50}Z")
    assert_equal false, matches("^(a+)+$", "#{"a" * 5000}!")
    assert_operator Process.clock_gettime(Process::CLOCK_MONOTONIC) - started, :<, 2
  end

  # A flag group opens no operand, so a repetition after it repeats what
  # came before, as in Go.
  def test_repetition_after_a_flag_group_repeats_the_previous_operand
    assert_equal true, matches("^a(?i)*$", "aaa")
    assert_equal true, matches("^a*(?i)*$", "aaa")
    assert_equal "invalid", matches("(?i)*", "a")
    assert_equal "invalid", matches("a|(?i)*", "a")
  end

  def test_limits_are_go_s
    assert_equal "invalid", matches("a{1001}", "a")
    assert_equal "invalid", matches("(?:a{1000}){2}", "a")
    assert_equal "invalid", matches("#{"(" * 1001}a#{")" * 1001}", "a")
    assert_equal true, matches("^a{1000}$", "a" * 1000)
  end

  private

  def matches(pattern, input) = RegexHelper.matches(pattern, input)
end
