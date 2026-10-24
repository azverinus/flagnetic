# frozen_string_literal: true

require "test_helper"

# Exposures: the explicit call, automatic exposure, the alias method,
# repeat suppression, and the queue's own behaviour online.
class ExposuresTest < Minitest::Test
  include ClientHelpers

  FIXTURE = JSON.parse(File.read(File.expand_path("../../../conformance/fixtures/experiments/automatic-exposure.json",
                                                  __dir__)))

  def offline(**)
    Flagnetic::Client.new(offline: FIXTURE["ruleset"], logger: method(:record), **)
  end

  ASSIGNED = { units: { "anonymous" => "u_10482" } }.freeze
  EXCLUDED = { units: { "anonymous" => "u_10483" } }.freeze
  OVERRIDDEN = { units: { "anonymous" => "u_10484" } }.freeze

  # --- automatic exposure --------------------------------------------------

  def test_automatic_exposure_emits_only_for_a_result_carrying_an_experiment
    c = offline
    c.string("auto_on", default: "", context: ASSIGNED) # automatic_exposure: true, reason experiment
    c.string("auto_on", default: "", context: EXCLUDED) # falls through: no experiment block
    exposures = c.recorder.exposures
    assert_equal 1, exposures.size
    e = exposures.first
    assert_equal %w[checkout_v2 auto_on control anonymous u_10482], [e.experiment_key, e.flag_key, e.variation_key,
                                                                     e.unit_type, e.unit_id]
    assert_equal [:experiment, false, 1], [e.reason, e.override, e.ruleset_version]
    assert_match(/\A[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\z/, e.event_id)
    assert_kind_of Time, e.occurred_at
  end

  def test_automatic_exposure_marks_an_override_into_the_experiment
    c = offline
    c.string("auto_on", default: "", context: OVERRIDDEN)
    e = c.recorder.exposures.first
    assert_equal ["off", true, :override], [e.variation_key, e.override, e.reason]
  end

  def test_a_flag_without_automatic_exposure_emits_nothing_on_its_own
    c = offline
    c.string("auto_off", default: "", context: ASSIGNED)
    assert_empty c.recorder.exposures
    assert_empty logged(:exposure_skipped)
  end

  # --- the explicit call ----------------------------------------------------

  def test_exposure_reports_the_most_recent_result_carrying_an_experiment
    c = offline
    c.string("auto_off", default: "", context: ASSIGNED)
    c.exposure("auto_off", context: ASSIGNED)
    assert_equal 1, c.recorder.exposures.size
    assert_equal "control", c.recorder.exposures.first.variation_key
  end

  def test_exposure_sends_nothing_for_a_flag_never_evaluated_with_an_experiment
    c = offline
    c.exposure("auto_off", context: ASSIGNED)
    assert_empty c.recorder.exposures
    assert_equal [[:debug, { flag: "auto_off", cause: "no_experiment" }]], logged(:exposure_skipped)
  end

  def test_exposure_sends_nothing_for_a_context_never_evaluated
    c = offline
    c.string("auto_off", default: "", context: ASSIGNED)
    c.exposure("auto_off", context: { units: { "anonymous" => "u_never_seen" } })
    assert_empty c.recorder.exposures
    assert_equal [[:debug, { flag: "auto_off", cause: "no_detail" }]], logged(:exposure_skipped)
  end

  def test_exposure_sends_nothing_once_the_ruleset_stops_carrying_an_experiment
    doc = JSON.generate(FIXTURE["ruleset"])
    c = Flagnetic::Client.new(offline: -> { doc }, logger: method(:record))
    c.string("auto_off", default: "", context: ASSIGNED)

    no_experiment = JSON.parse(doc)
    flag = no_experiment["flags"].find { |f| f["key"] == "auto_off" }
    flag["rules"] = []
    doc = JSON.generate(no_experiment.merge("version" => 2))
    c.reload

    c.string("auto_off", default: "", context: ASSIGNED)
    c.exposure("auto_off", context: ASSIGNED)
    assert_empty c.recorder.exposures
  end

  def test_a_local_override_never_emits_an_exposure
    c = offline(allow_overrides: true)
    c.local_overrides.set("auto_on", "forced")
    c.string("auto_on", default: "", context: ASSIGNED) # would auto-emit, but the override wins first
    c.exposure("auto_on", context: ASSIGNED)
    assert_empty c.recorder.exposures
  end

  def test_exposure_and_alias_never_raise
    c = offline
    assert_nil c.exposure(nil, context: Object.new)
    assert_nil c.alias(nil, nil, nil, nil)
  end

  # --- platform and app version ------------------------------------------

  def test_an_exposure_takes_platform_and_app_version_from_the_context_then_the_options
    c = offline(platform: "worker", app_version: "2.0.0")
    c.string("auto_on", default: "", context: ASSIGNED)
    named = ASSIGNED.merge(attributes: { "platform" => "linux", "app_version" => "1.18.0" })
    c.string("auto_on", default: "", context: named.merge(units: { "anonymous" => "u_10484" }))
    assert_equal([%w[worker 2.0.0], %w[linux 1.18.0]], c.recorder.exposures.map { |e| [e.platform, e.app_version] })
  end

  # --- repeat suppression ---------------------------------------------------

  def test_a_repeat_of_the_same_experiment_unit_and_variation_is_suppressed
    c = offline
    3.times do
      c.string("auto_off", default: "", context: ASSIGNED)
      c.exposure("auto_off", context: ASSIGNED)
    end
    assert_equal 1, c.recorder.exposures.size
    assert_equal(2, logged(:exposure_skipped).count { |(_, f)| f[:cause] == "repeat" })
  end

  def test_a_repeat_is_sent_again_once_the_suppression_window_passed
    c = offline(flush_interval: 0.05)
    c.string("auto_on", default: "", context: ASSIGNED)
    sleep 0.06
    c.string("auto_on", default: "", context: ASSIGNED)
    sleep 0.06
    c.string("auto_on", default: "", context: ASSIGNED)
    assert_equal 2, c.recorder.exposures.size
  end

  # --- alias ------------------------------------------------------------

  def test_alias_records_an_event_linking_two_unit_ids
    c = offline
    c.alias("anonymous", "d-8f1c", "user", "u-1001")
    a = c.recorder.aliases.first
    assert_equal %w[anonymous d-8f1c user u-1001], [a.from_unit_type, a.from_unit_id, a.to_unit_type, a.to_unit_id]
    assert_match(/\A[0-9a-f-]{36}\z/, a.event_id)
  end

  # --- send_exposures -------------------------------------------------------

  def test_send_exposures_false_sends_nothing_and_warns_at_construction
    c = offline(send_exposures: false)
    c.string("auto_on", default: "", context: ASSIGNED)
    c.alias("anonymous", "d-8f1c", "user", "u-1001")
    assert_empty c.recorder.exposures
    assert_empty c.recorder.aliases
    assert_equal [[:warn, {}]], logged(:exposures_disabled)
  end

  # --- the queue (online) -------------------------------------------------

  def test_events_are_sent_gzipped_with_the_envelope_on_the_flush_interval
    @server.publish(document(1))
    c = client(flush_interval: 0.05, app_version: "3.1.0")
    c.ready(timeout: 2)
    c.alias("anonymous", "d-8f1c", "user", "u-1001")
    assert(eventually(2) { @server.events_requests.any? })
    request = @server.events_requests.first
    assert_equal "gzip", request["content-encoding"]
    assert_equal "application/json", request["content-type"]
    assert_equal "1", request["flagnetic-schema-version"]
    doc = JSON.parse(request.decoded_body)
    assert_equal({ "name" => "flagnetic-ruby", "version" => Flagnetic::VERSION, "platform" => "server" }, doc["sdk"])
    assert_equal 1, doc["schema_version"]
    assert_equal(%w[alias], doc["events"].map { |e| e["type"] })
  end

  def test_the_queue_waits_for_the_flush_interval_then_sends_one_batch
    @server.publish(document(1))
    c = client(flush_interval: 0.3, stream: false)
    c.ready(timeout: 2)
    3.times { |i| c.alias("anonymous", "d-#{i}", "user", "u-#{i}") }
    sleep 0.1
    assert_empty @server.events_requests, "sent before the flush interval"
    assert(eventually(2) { @server.events_requests.any? })
    assert_equal([3], @server.events_requests.map { |r| r.events.size })
  end

  def test_a_full_batch_is_sent_without_waiting_for_the_interval
    @server.publish(document(1))
    c = client(flush_interval: 3600, stream: false)
    c.ready(timeout: 2)
    500.times { |i| c.alias("anonymous", "d-#{i}", "user", "u-#{i}") }
    assert(eventually(2) { @server.events_requests.any? })
    assert_equal 500, @server.events_requests.first.events.size
  end

  # An alias enqueued while the worker is settled into a long, unrelated
  # wait (no stream traffic, a distant poll) must still be flushed near
  # flush_interval, not whenever that unrelated deadline next fires.
  def test_enqueuing_wakes_a_worker_settled_into_a_longer_wait
    @server.publish(document(1))
    c = client(flush_interval: 0.05, poll_interval: 3600, stream: false)
    c.ready(timeout: 2)
    sleep 0.2 # let the worker settle into its wait for the long poll deadline
    c.alias("anonymous", "d-1", "user", "u-1")
    assert(eventually(1) { @server.events_requests.any? })
  end

  def test_the_queue_drops_the_oldest_events_past_capacity
    @server.on_events { |_| [500, {}, ""] }
    @server.publish(document(1))
    c = client(flush_interval: 3600, stream: false)
    c.ready(timeout: 2)
    10_002.times { |i| c.alias("anonymous", "d-#{i}", "user", "u-#{i}") }
    # Full batches leave for the failing server while the queue fills; the
    # excess is dropped either then or when the unsent batches come back.
    full = -> { logged(:queue_dropped).select { |(_, f)| f[:cause] == "queue_full" } }
    assert(eventually(5) { full.call.any? })
    dropped = full.call
    assert(dropped.all? { |(level, f)| level == :warn && f[:kind] == "exposure_or_alias" && f[:capacity] == 10_000 })
  end

  def test_a_rejected_event_is_never_sent_again_and_is_logged
    @server.on_events do |request|
      ids = request.events.map { |e| e["event_id"] }
      [202, { "Content-Type" => "application/json" },
       JSON.generate({ "accepted" => 0, "duplicates" => 0, "dropped_rows" => 0,
                       "rejected" => [{ "index" => 0, "event_id" => ids[0], "code" => "out_of_window",
                                        "detail" => "too old" }] })]
    end
    @server.publish(document(1))
    c = client(stream: false)
    c.ready(timeout: 2)
    c.alias("anonymous", "d-8f1c", "user", "u-1001")
    c.flush(timeout: 2)
    c.flush(timeout: 2)
    assert_equal 1, @server.events_requests.size
    assert_equal ["out_of_window"], logged(:queue_dropped).first[1][:rejected]
  end

  def test_another_4xx_drops_the_batch
    @server.on_events { |_| [400, {}, ""] }
    @server.publish(document(1))
    c = client(stream: false)
    c.ready(timeout: 2)
    c.alias("anonymous", "d-8f1c", "user", "u-1001")
    c.flush(timeout: 2)
    c.flush(timeout: 2)
    assert_equal 1, @server.events_requests.size
    assert_equal "client_error", logged(:queue_dropped).first[1][:cause]
  end

  def test_a_refused_key_logs_key_revoked_once_and_keeps_the_events
    @server.on_events { |_| [401, {}, ""] }
    @server.publish(document(1))
    c = client(stream: false)
    c.ready(timeout: 2)
    c.alias("anonymous", "d-8f1c", "user", "u-1001")
    c.flush(timeout: 0.3)
    c.flush(timeout: 0.3)
    assert_equal [[:error, { status: 401, source: "events" }]], logged(:key_revoked)

    @server.on_events { |_| nil }
    c.flush(timeout: 2)
    assert_equal 1, @server.events_requests.last.events.size
  end

  def test_close_flushes_within_its_timeout_and_returns_promptly_during_backoff
    @server.on_events { |_| [500, {}, ""] }
    @server.publish(document(1))
    c = client
    c.ready(timeout: 2)
    c.alias("anonymous", "d-8f1c", "user", "u-1001")
    started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
    c.close(timeout: 1)
    took = Process.clock_gettime(Process::CLOCK_MONOTONIC) - started
    assert_operator took, :<, 3
    refute_empty @server.events_requests
    @clients.delete(c)
  end

  def test_close_sends_what_is_queued
    @server.publish(document(1))
    c = client(flush_interval: 3600)
    c.ready(timeout: 2)
    c.alias("anonymous", "d-8f1c", "user", "u-1001")
    c.close(timeout: 2)
    assert_equal 1, @server.events_requests.size
    @clients.delete(c)
  end
end
