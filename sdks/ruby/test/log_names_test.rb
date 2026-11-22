# frozen_string_literal: true

require "test_helper"

# Operators alert on log names, so every SDK logs the same ones: the names
# the SDK guide lists are in Flagnetic::EVENTS, and the names they replaced
# are logged nowhere.
class LogNamesTest < Minitest::Test
  GUIDE = File.expand_path("../../../docs/sdks/conformance.md", __dir__)
  LIB = File.expand_path("../lib", __dir__)
  REPLACED = %w[ingest_failed exposures_disabled sse_connected sse_dropped window_older_than_one_hour].freeze

  # documented reads the first column of the table under "Log names".
  def documented
    section = File.read(GUIDE)[/^## Log names\n(.*?)(?=^## )/m, 1]
    refute_nil section, "no Log names section in #{GUIDE}"
    section.scan(/^\| `([a-z_]+)` \|/).flatten
  end

  def test_every_documented_name_is_an_event
    names = documented
    refute_empty names
    assert_empty names.map(&:to_sym) - Flagnetic::EVENTS, "documented but not in Flagnetic::EVENTS"
  end

  def test_no_replaced_name_is_left_in_the_library
    found = Dir[File.join(LIB, "**", "*.rb")].flat_map do |path|
      text = File.read(path)
      REPLACED.select { |name| text.include?(name) }.map { |name| "#{File.basename(path)}: #{name}" }
    end
    assert_empty found
  end
end
