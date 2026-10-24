# frozen_string_literal: true

require "test_helper"
require "minitest/mock"

# Evaluation summaries: counts by (flag, variation, reason), fallback and
# local_override included, reported on the interval or on flush, with a
# failed report's window merged into the next one.
class SummariesTest < Minitest::Test
  include ClientHelpers

  def summaries(request) = request.events.select { |e| e["type"] == "evaluation_summary" }

  def rows(summary) = summary["counts"].to_h { |r| [[r["flag"], r["variation"], r["reason"]], r["count"]] }

  # --- offline: the recorder ----------------------------------------------

  def test_the_offline_recorder_reports_a_window_on_flush_with_the_ruleset_version_and_no_unit_ids
    c = Flagnetic::Client.new(offline: document(7, { "on" => true }), app_version: "1.2.3")
    c.bool("on", default: false, context: { units: { user: "u-1001" } })
    c.bool("on", default: false, context: { units: { user: "u-1002" } })
    c.bool("missing", default: false)
    c.flush(timeout: 1)

    summaries = c.recorder.evaluation_summaries
    assert_equal 1, summaries.size
    s = summaries.first
    assert_equal [7, "1.2.3"], [s.ruleset_version, s.app_version]
    assert_operator s.interval_start, :<=, s.interval_end
    assert_match(/\A[0-9a-f-]{36}\z/, s.event_id)
    rows = s.counts.map { |r| [r.flag, r.variation, r.reason, r.count] }
    assert_includes rows, ["on", "v", :fallthrough, 2]
    assert_includes rows, ["missing", nil, :fallback, 1]
    refute_includes rows.flatten.map(&:to_s), "u-1001"
  end

  def test_a_second_flush_with_nothing_evaluated_sends_nothing
    c = Flagnetic::Client.new(offline: document(1, { "on" => true }))
    c.bool("on", default: false)
    c.flush(timeout: 1)
    c.flush(timeout: 1)
    assert_equal 1, c.recorder.evaluation_summaries.size
  end

  def test_send_summaries_false_counts_and_sends_nothing
    c = Flagnetic::Client.new(offline: document(1, { "on" => true }), send_summaries: false)
    c.bool("on", default: false)
    c.flush(timeout: 1)
    assert_empty c.recorder.evaluation_summaries
  end

  def test_an_int_that_is_not_whole_is_counted_as_the_fallback_it_served
    c = Flagnetic::Client.new(offline: document(1, { "ratio" => 2.5 }), logger: method(:record))
    c.int("ratio", default: 1)
    c.flush(timeout: 1)
    assert_equal([["ratio", nil, :fallback, 1]],
                 c.recorder.evaluation_summaries.first.counts.map { |r| [r.flag, r.variation, r.reason, r.count] })
  end

  def test_rows_beyond_a_thousand_go_into_further_summaries_of_the_same_window
    values = (1..1001).to_h { |i| ["f#{i}", true] }
    c = Flagnetic::Client.new(offline: document(1, values))
    values.each_key { |k| c.bool(k, default: false) }
    c.flush(timeout: 1)
    summaries = c.recorder.evaluation_summaries
    assert_equal([1000, 1], summaries.map { |s| s.counts.size })
    assert_equal 1, summaries.map(&:interval_start).uniq.size
    assert_equal 2, summaries.map(&:event_id).uniq.size
  end

  # --- online: interval, retry and the merged failed window --------------

  def test_summaries_flush_online_and_report_counts_equal_to_evaluations
    @server.publish(document(1, { "on" => true }))
    c = client(summary_interval: 3600, app_version: "4.0.0")
    c.ready(timeout: 2)
    3.times { c.bool("on", default: false) }
    c.flush(timeout: 2)

    summary = summaries(@server.events_requests.first).first
    refute_nil summary
    assert_equal [1, "4.0.0"], summary.values_at("ruleset_version", "app_version")
    assert_equal({ %w[on v fallthrough] => 3 }, rows(summary))
  end

  def test_a_summary_without_app_version_leaves_the_member_out
    @server.publish(document(1, { "on" => true }))
    c = client(summary_interval: 3600)
    c.ready(timeout: 2)
    c.bool("on", default: false)
    c.flush(timeout: 2)
    refute summaries(@server.events_requests.first).first.key?("app_version")
  end

  def test_summaries_are_sent_on_the_summary_interval
    @server.publish(document(1, { "on" => true }))
    c = client(summary_interval: 0.1, stream: false)
    c.ready(timeout: 2)
    c.bool("on", default: false)
    assert(eventually(2) { @server.events_requests.any? })
    assert_equal({ %w[on v fallthrough] => 1 }, rows(summaries(@server.events_requests.first).first))
  end

  def test_evaluating_before_any_ruleset_reports_version_zero
    @server.on_ruleset { |_| [500, {}, ""] }
    c = client(summary_interval: 3600, stream: false)
    c.bool("on", default: false)
    c.flush(timeout: 2)
    summary = summaries(@server.events_requests.first).first
    assert_equal 0, summary["ruleset_version"]
    assert_equal({ ["on", nil, "fallback"] => 1 }, rows(summary))
  end

  def test_a_failed_report_merges_into_the_next_window
    @server.on_events { |_| [500, {}, ""] }
    @server.publish(document(1, { "on" => true }))
    c = client(summary_interval: 3600, stream: false)
    c.ready(timeout: 2)
    4.times { c.bool("on", default: false) }
    c.flush(timeout: 0.3)
    failed = @server.events_requests.dup
    refute_empty failed

    @server.on_events { |_| nil }
    2.times { c.bool("on", default: false) }
    c.flush(timeout: 2)
    merged = summaries(@server.events_requests.last).first
    assert_equal({ %w[on v fallthrough] => 6 }, rows(merged))
    refute_includes failed.flat_map { |r| r.events.map { |e| e["event_id"] } }, merged["event_id"]
    assert_equal summaries(failed.first).first["interval_start"], merged["interval_start"]
  end

  def test_a_window_older_than_an_hour_is_dropped_rather_than_merged
    @server.on_events { |_| [500, {}, ""] }
    @server.publish(document(1, { "on" => true }))
    c = client(summary_interval: 3600, stream: false)
    c.ready(timeout: 2)
    4.times { c.bool("on", default: false) }
    c.flush(timeout: 0.3)

    @server.on_events { |_| nil }
    later = Time.now + 3700
    Time.stub(:now, later) do
      c.bool("on", default: false)
      c.flush(timeout: 2)
    end
    assert_equal({ %w[on v fallthrough] => 1 }, rows(summaries(@server.events_requests.last).first))
    dropped = logged(:queue_dropped).map(&:last)
    assert_equal(["window_older_than_one_hour"], dropped.map { |f| f[:cause] })
  end

  def test_events_are_retried_on_5xx_with_the_same_event_id
    attempts = 0
    @server.on_events do |_|
      attempts += 1
      attempts == 1 ? [500, {}, ""] : nil
    end
    @server.publish(document(1, { "on" => true }))
    c = client(summary_interval: 3600)
    c.ready(timeout: 2)
    c.bool("on", default: false)
    c.flush(timeout: 2)

    ids = @server.events_requests.map { |r| r.events.first["event_id"] }
    assert_equal 2, ids.size
    assert_equal 1, ids.uniq.size
  end

  def test_a_429_is_retried_after_its_retry_after
    attempts = 0
    @server.on_events do |_|
      attempts += 1
      attempts == 1 ? [429, { "Retry-After" => "1" }, ""] : nil
    end
    @server.publish(document(1, { "on" => true }))
    c = client(summary_interval: 3600, timing: { fetch_cap: 5 })
    c.ready(timeout: 2)
    c.bool("on", default: false)
    c.flush(timeout: 3)

    first, second = @server.events_requests
    refute_nil second
    assert_operator second.at - first.at, :>=, 0.95
  end

  def test_a_background_flush_through_an_events_outage_does_not_hold_back_a_flag_change
    @server.on_events { |_| [503, {}, ""] }
    @server.publish(document(1, { "on" => false }))
    c = client(summary_interval: 1.5, timing: { request_timeout: 0.3 })
    c.ready(timeout: 2)
    c.bool("on", default: true)
    assert(eventually(3) { @server.events_requests.any? })

    @server.publish(document(2, { "on" => true }))
    assert(eventually(0.8) { c.bool("on", default: false) })
  end

  def test_close_flushes_and_stops_within_its_one_timeout
    @server.publish(document(1, { "on" => true }))
    c = client(summary_interval: 3600, stream: false, timing: { poll_down: 0.05 })
    c.ready(timeout: 2)
    @server.on_events do |_|
      sleep 2
      nil
    end
    @server.on_ruleset do |_|
      sleep 2
      nil
    end
    c.bool("on", default: false)
    sleep 0.1 # the background thread is now inside a fetch that hangs

    started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
    c.close(timeout: 0.5)
    assert_operator Process.clock_gettime(Process::CLOCK_MONOTONIC) - started, :<, 0.8
  end
end
