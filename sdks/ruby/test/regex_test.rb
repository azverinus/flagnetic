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

  # Go 1.27 canonicalises script names as it does category aliases, so a
  # script of several words is read under any spelling. The shared vectors
  # leave these out because Go 1.26 refuses them.
  def test_a_script_of_several_words_is_read_under_any_spelling_the_server_reads
    ["\\p{Old_Italic}", "\\p{old italic}", "\\p{OLD-ITALIC}", "\\p{OldItalic}", "\\p{oLd_iTaLiC}",
     "[\\p{Old_Italic}x]", "(?i)\\p{Old_Italic}"].each do |pattern|
      assert matches(pattern, "\u{10300}"), "pattern #{pattern}"
      assert_equal false, matches(pattern, "a"), "pattern #{pattern}"
    end
    assert_equal true, matches("\\P{Old_Italic}", "a")
    assert_equal true, matches("\\p{^Old_Italic}", "a")
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

  # A fiber's stack is a fraction of a thread's; compiling a deeply nested
  # pattern must not depend on it.
  def test_deeply_nested_pattern_compiles_on_a_fiber_stack
    pattern = "^#{"(?:a" * 300}#{")*" * 300}$"

    assert_equal true, Fiber.new { matches(pattern, "aaa") }.resume
  end

  def test_an_input_over_64_kib_is_a_non_match_and_one_at_64_kib_is_matched
    limit = 64 << 10
    assert_equal true, matches("b$", "#{"a" * (limit - 1)}b")
    assert_equal false, matches("b$", "#{"a" * limit}b")
    assert_equal false, matches(".", "a" * (limit + 1)), "no error, just no match"
    # The limit is in bytes: 32769 two-byte characters are over it.
    assert_equal false, matches("é", "é" * ((limit / 2) + 1))
    assert_equal true, matches("é", "é" * (limit / 2))
  end

  private

  def matches(pattern, input) = RegexHelper.matches(pattern, input)
end
