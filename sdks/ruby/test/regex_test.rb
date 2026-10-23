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

  private

  def matches(pattern, input) = RegexHelper.matches(pattern, input)
end
