# frozen_string_literal: true

require "test_helper"

# InstantTest pins date parsing to what the reference evaluator's
# time.Parse(time.RFC3339Nano) accepts, including its leniencies (a comma
# before the fraction, a one-digit hour, an offset of up to 24:60). Each
# vector is an input and the instant Go reads, in Unix nanoseconds, or nil
# when Go refuses it.
class InstantTest < Minitest::Test
  Instant = Flagnetic::Eval.const_get(:Instant)

  VECTORS = [
    ["2026-09-19T12:00:00Z", 1_789_819_200_000_000_000],
    ["2026-09-19t12:00:00z", nil],
    ["2026-09-19T12:00:00,5Z", 1_789_819_200_500_000_000],
    ["2026-09-19T12:00:00.Z", nil],
    ["2026-09-19T12:00:00.1234567891Z", 1_789_819_200_123_456_789],
    ["2026-09-19T24:00:00Z", nil],
    ["2026-09-19T12:00:60Z", nil],
    ["2026-02-29T12:00:00Z", nil],
    ["2024-02-29T12:00:00Z", 1_709_208_000_000_000_000],
    ["1900-02-29T12:00:00Z", nil],
    ["2000-02-29T12:00:00Z", 951_825_600_000_000_000],
    ["2026-09-19T12:00:00+24:00", 1_789_732_800_000_000_000],
    ["2026-09-19T12:00:00-00:00", 1_789_819_200_000_000_000],
    ["2026-09-19T12:00:00+0300", nil],
    ["2026-09-19 12:00:00Z", nil],
    ["2026-9-19T12:00:00Z", nil],
    ["12026-09-19T12:00:00Z", nil],
    ["0001-01-01T00:00:00Z", -62_135_596_800_000_000_000],
    ["2026-09-19T12:00:00+03:60", 1_789_804_800_000_000_000],
    ["2026-09-19T12:00:00+03:61", nil],
    ["2026-09-19T12:00:00+24:60", 1_789_729_200_000_000_000],
    ["2026-09-19T12:00:00+25:00", nil],
    ["2026-09-19T12:00:00.123-03:30", 1_789_831_800_123_000_000],
    [" 2026-09-19T12:00:00Z", nil],
    ["2026-09-19T1:00:00Z", 1_789_779_600_000_000_000],
    ["2026-09-19T12:0:00Z", nil],
    ["2026-09-31T12:00:00Z", nil],
    ["2026-00-10T12:00:00Z", nil],
    ["2026-09-19T12:00:00.5,5Z", nil],
    ["1969-12-31T23:59:59.999999999Z", -1]
  ].freeze

  def test_rfc3339_reads_like_go
    VECTORS.each do |input, want|
      got = Instant.parse_rfc3339(input)
      want.nil? ? assert_nil(got, input) : assert_equal(want, got, input)
    end
  end

  def test_context_values
    noon = 1_789_819_200_000_000_000
    assert_equal noon, Instant.from_context(Time.utc(2026, 9, 19, 12))
    assert_equal noon + 1, Instant.from_context(Time.at(1_789_819_200, 1, :nsec))
    assert_equal noon, Instant.from_context(1_789_819_200_000)
    assert_equal noon, Instant.from_context(1_789_819_200_000.0)
    assert_nil Instant.from_context(1.5)
    assert_nil Instant.from_context(2**54) # beyond 2^53 milliseconds
    assert_nil Instant.from_context(true)
    assert_nil Instant.from_context("1789819200000")
  end
end
