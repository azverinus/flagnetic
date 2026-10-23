# frozen_string_literal: true

require "test_helper"
require_relative "support/large_ruleset"

# P99Test keeps the evaluation of one flag of a 1,000-flag ruleset under a
# millisecond at the 99th percentile, so a flag costs nothing on a hot path.
# The measured figure goes to the job summary of the CI run that measures it.
class P99Test < Minitest::Test
  LIMIT = 0.001
  CALLS = 5000

  def test_one_flag_of_a_thousand_evaluates_under_a_millisecond_at_p99
    ruleset = Flagnetic::Eval::Ruleset.decode(LargeRuleset.document)
    contexts = Array.new(CALLS) { |i| LargeRuleset.context(i) }
    keys = Array.new(CALLS) { |i| "flag_#{(i * 7919) % LargeRuleset::FLAGS}" }
    reasons = Hash.new(0)
    CALLS.times { |i| ruleset.evaluate(keys[i], contexts[i]) } # warm up

    durations = Array.new(CALLS) do |i|
      start = Process.clock_gettime(Process::CLOCK_MONOTONIC)
      reasons[ruleset.evaluate(keys[i], contexts[i]).reason] += 1
      Process.clock_gettime(Process::CLOCK_MONOTONIC) - start
    end
    p99 = durations.sort[(CALLS * 0.99).ceil - 1]
    summarize(p99)

    assert_operator p99, :<, LIMIT, "p99 is #{micros(p99)} µs"
    # The ruleset exercises rollouts, experiments and failed prerequisites.
    assert_operator reasons.keys.size, :>=, 3, reasons.inspect
  end

  private

  def micros(seconds) = (seconds * 1_000_000).round(1)

  def summarize(p99)
    line = "Ruby #{RUBY_VERSION}: evaluation p99 #{micros(p99)} µs over #{CALLS} calls " \
           "in #{LargeRuleset::FLAGS} flags (limit #{(LIMIT * 1_000_000).to_i} µs)"
    puts line
    path = ENV.fetch("GITHUB_STEP_SUMMARY", nil)
    File.write(path, "#{line}\n\n", mode: "a") if path
  end
end
