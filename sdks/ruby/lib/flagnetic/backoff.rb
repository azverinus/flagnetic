# frozen_string_literal: true

module Flagnetic
  # Backoff computes retry delays: exponential from base, capped, with full
  # jitter, so a platform restart does not bring every SDK back at once.
  # The n-th delay is uniform between zero and min(cap, base * 2**n).
  class Backoff
    def initialize(base:, cap:, random: Random.new)
      @base = base
      @cap = cap
      @random = random
      @attempts = 0
    end

    # next_delay returns the next delay in seconds. cap overrides the
    # configured cap, for the unauthorized cap of a revoked key.
    def next_delay(cap: @cap)
      ceiling = @attempts > 62 ? cap : [cap, @base * (2**@attempts)].min
      @attempts += 1
      @random.rand * ceiling
    end

    # reset starts over from the base, after a success.
    def reset
      @attempts = 0
    end
  end
  private_constant :Backoff
end
