# frozen_string_literal: true

require "test_helper"

# The ruleset fetch and the fallback chain: what is accepted, what never
# replaces the held ruleset, and how the stage reports it.
class ClientFetchTest < Minitest::Test
  include ClientHelpers

  def test_construction_fails_loudly_only_for_a_client_key
    error = assert_raises(ArgumentError) { Flagnetic::Client.new("fn_cli_#{"a" * 40}", base_url: @server.base_url) }
    assert_match(/server key/, error.message)

    c = client # nothing published: the server answers 503
    assert_equal :built_in, c.stage
    d = c.detail("anything", default: true)
    assert_equal [true, :fallback, :no_ruleset, :built_in], [d.value, d.reason, d.error_code, d.stage]
  end

  def test_an_unreachable_server_never_fails_construction
    c = Flagnetic::Client.new("fn_srv_x", base_url: "http://127.0.0.1:1", logger: method(:record),
                                          timing: FAST)
    @clients << c
    assert_equal :built_in, c.ready(timeout: 2)
    assert_equal "off", c.string("copy", default: "off")
    refute_empty logged(:fetch_failed)
  end

  def test_the_fetch_request_and_a_304_revalidation
    @server.gzip!
    @server.publish(document(1, { "on" => true }))
    c = client(stream: false, timing: { poll_down: 0.1 })
    assert_equal :fresh, c.ready(timeout: 2)
    assert c.bool("on", default: false)

    eventually { @server.fetches.size >= 2 }
    first, second = @server.fetches
    assert_equal "Bearer fn_srv_#{"a" * 40}", first["authorization"]
    assert_equal "flagnetic-ruby/#{Flagnetic::VERSION} (server)", first["user-agent"]
    assert_equal "1", first["flagnetic-schema-version"]
    assert_match(/gzip/, first["accept-encoding"])
    assert_nil first["if-none-match"]
    assert_equal '"1"', second["if-none-match"]
    assert_equal :fresh, c.stage
    assert_equal 1, logged(:ruleset_loaded).size, "a 304 loads nothing"
  end

  def test_ready_resolves_built_in_at_the_timeout_and_a_late_200_moves_to_fresh
    @server.publish(document(1, { "on" => true }))
    @server.on_ruleset do |_|
      sleep 0.6 if @server.fetches.size == 1
      nil
    end
    c = client(timing: { request_timeout: 3 })
    stages = Queue.new
    c.on_stage { |s| stages << s }

    started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
    assert_equal :built_in, c.ready(timeout: 0.2)
    assert_in_delta 0.2, Process.clock_gettime(Process::CLOCK_MONOTONIC) - started, 0.15
    refute c.bool("on", default: false), "built-in default before the ruleset arrives"

    assert_equal :fresh, pop(stages, 3)
    assert c.bool("on", default: false)
    assert_equal :fresh, c.ready(timeout: 0)
  end

  def test_a_500_after_a_good_ruleset_serves_last_good_and_recovers
    @server.publish(document(1, { "on" => true }))
    failures = 0
    armed = false
    @server.on_ruleset do |_|
      next nil if !armed || failures >= 2

      failures += 1
      [500, {}, "boom"]
    end
    c = client(stream: false, timing: { poll_down: 0.05 })
    assert_equal :fresh, c.ready(timeout: 2)
    stages = Queue.new
    c.on_stage { |s| stages << s }
    armed = true

    assert_equal :last_good, pop(stages, 2)
    d = c.detail("on", default: false)
    assert_equal [true, :fallthrough, :last_good, 1], [d.value, d.reason, d.stage, d.ruleset_version]
    level, fields = logged(:fetch_failed).first
    assert_equal [:warn, "HTTP 500"], [level, fields[:cause]]
    assert_equal :fresh, pop(stages, 2)
    assert_equal [:info, { from: :built_in, to: :fresh }], logged(:stage_changed).first
    assert_equal [:warn, { from: :fresh, to: :last_good }], logged(:stage_changed)[1]
  end

  REFUSED = {
    "an empty body" => "",
    "{}" => "{}",
    "truncated JSON" => '{"schema_version":1,',
    "a JSON array" => "[]",
    "a missing schema_version" => '{"version":5,"scope":"server","flags":[]}',
    "a wrong shape" => '{"schema_version":1,"version":5,"scope":"server","flags":{}}',
    "a lower version" => '{"schema_version":1,"version":1,"scope":"server","flags":[]}',
    "a client-scope document" => '{"schema_version":1,"version":5,"scope":"client","flags":[]}',
    "an unsupported schema_version" => '{"schema_version":2,"version":5,"scope":"server","flags":[]}',
    "a schema_version below the range" => '{"schema_version":0,"version":5,"scope":"server","flags":[]}'
  }.freeze

  REFUSED.each_with_index do |(name, body), i|
    define_method(:"test_#{i}_#{name.tr(" -", "__")}_never_replaces_the_held_ruleset") do
      @server.publish(document(2, { "on" => true }))
      @server.on_ruleset { |_| @server.fetches.size == 1 ? nil : [200, {}, body] }
      c = client(stream: false, timing: { poll_down: 0.05 })
      assert_equal :fresh, c.ready(timeout: 2)

      eventually { logged(:fetch_failed).size >= 3 }
      d = c.detail("on", default: false)
      assert_equal [true, :fallthrough, :last_good, 2], [d.value, d.reason, d.stage, d.ruleset_version]
      refute_empty logged(:fetch_failed)
    end
  end

  def test_an_unsupported_schema_is_logged_once
    @server.publish(document(2, { "on" => true }))
    @server.on_ruleset { |_| @server.fetches.size == 1 ? nil : [200, {}, REFUSED["an unsupported schema_version"]] }
    c = client(stream: false, timing: { poll_down: 0.05 })
    c.ready(timeout: 2)
    eventually { logged(:fetch_failed).size >= 3 }
    assert_equal [[:error, { schema_version: 2, supported: [1] }]], logged(:schema_unsupported)
    assert_match(/schema_version 2/, logged(:fetch_failed).first[1][:cause])
  end

  def test_parse_failures_name_the_cause
    @server.on_ruleset { |_| [200, {}, "{"] }
    c = client(stream: false)
    assert_equal :built_in, c.ready(timeout: 2)
    assert_match(/\Aparse: /, logged(:fetch_failed).first[1][:cause])
  end

  def test_an_empty_ruleset_is_fresh_and_every_getter_serves_its_default
    @server.publish(document(1))
    c = client
    assert_equal :fresh, c.ready(timeout: 2)
    d = c.detail("missing", default: "x")
    assert_equal ["x", :fallback, :flag_not_found, :fresh], [d.value, d.reason, d.error_code, d.stage]
  end

  def test_a_revoked_key_keeps_serving_and_retries_at_the_unauthorized_cap
    @server.publish(document(1, { "on" => true }))
    revoked = false
    @server.on_ruleset { |_| revoked ? [401, {}, ""] : nil }
    c = client(stream: false, timing: { poll_down: 0.05 })
    assert_equal :fresh, c.ready(timeout: 2)

    revoked = true
    eventually { @server.fetches.size >= 6 }
    assert_equal [[:error, { status: 401, source: "fetch" }]], logged(:key_revoked), "logged once"
    assert_equal :last_good, c.stage
    assert c.bool("on", default: false)
    gaps = @server.fetches.drop(1).each_cons(2).map { |a, b| b.at - a.at }
    assert gaps.all? { |g| g <= 0.4 + 0.15 }, "every retry within the unauthorized cap: #{gaps}"

    revoked = false
    eventually { c.stage == :fresh }
    assert_equal :fresh, c.stage
  end

  def test_retries_back_off_within_the_jitter_bounds
    @server.on_ruleset { |_| [503, {}, ""] }
    client(stream: false, timing: { fetch_base: 0.02, fetch_cap: 0.16 })
    eventually(3) { @server.fetches.size >= 12 }
    gaps = @server.fetches.each_cons(2).map { |a, b| b.at - a.at }
    gaps.each_with_index do |gap, i|
      assert_operator gap, :<=, [0.02 * (2**i), 0.16].min + 0.15, "retry #{i} waited #{gap}"
    end
  end

  def test_polling_uses_the_short_interval_while_the_stream_is_down
    @server.publish(document(1))
    @server.on_stream { |_| [404, {}, ""] }
    client(timing: { poll_down: 0.1, poll_connected: 10 })
    eventually { @server.fetches.size >= 4 }
    assert_operator @server.fetches.size, :>=, 4
  end

  def test_polling_uses_the_long_interval_while_the_stream_is_connected
    @server.publish(document(1))
    c = client(timing: { poll_down: 0.05, poll_connected: 10 })
    c.ready(timeout: 2)
    sleep 0.5
    assert_equal 1, @server.fetches.size
  end
end
