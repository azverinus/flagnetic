# frozen_string_literal: true

require "test_helper"

# Retries back off exponentially with full jitter: a delay is uniform
# between zero and the doubled base, capped.
class BackoffTest < Minitest::Test
  Backoff = Flagnetic.const_get(:Backoff)

  # A random source returning its fixed fraction.
  class Fixed
    def initialize(fraction) = @fraction = fraction
    def rand = @fraction
  end

  def delays(backoff, count, **opts) = Array.new(count) { backoff.next_delay(**opts) }

  def test_the_ceiling_doubles_from_the_base_up_to_the_cap
    backoff = Backoff.new(base: 1, cap: 300, random: Fixed.new(1.0))
    assert_equal [1, 2, 4, 8, 16, 32, 64, 128, 256, 300, 300], delays(backoff, 11)
  end

  def test_full_jitter_spreads_from_zero
    backoff = Backoff.new(base: 1, cap: 300, random: Fixed.new(0.25))
    assert_equal [0.25, 0.5, 1.0], delays(backoff, 3)
    assert_equal 0.0, Backoff.new(base: 1, cap: 300, random: Fixed.new(0.0)).next_delay
  end

  def test_real_delays_stay_within_bounds
    backoff = Backoff.new(base: 1, cap: 30)
    200.times.map { backoff.next_delay }.each_with_index do |d, i|
      assert_operator d, :>=, 0
      assert_operator d, :<=, [30, 2**i].min
    end
  end

  def test_a_larger_cap_lets_the_ceiling_grow_further
    backoff = Backoff.new(base: 1, cap: 300, random: Fixed.new(1.0))
    assert_equal 900, delays(backoff, 12, cap: 900).last
  end

  def test_reset_starts_over
    backoff = Backoff.new(base: 1, cap: 300, random: Fixed.new(1.0))
    delays(backoff, 5)
    backoff.reset
    assert_equal 1, backoff.next_delay
  end
end
