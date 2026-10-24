# frozen_string_literal: true

require "test_helper"

# The change stream: events trigger fetches, drops reconnect with
# Last-Event-ID, silence reconnects, a revoked key closes it.
class ClientStreamTest < Minitest::Test
  include ClientHelpers

  def now = Process.clock_gettime(Process::CLOCK_MONOTONIC)

  def connected_client(**)
    @server.publish(document(1, { "a" => true, "b" => "x" }))
    c = client(**)
    assert_equal :fresh, c.ready(timeout: 2)
    eventually { @server.streams.size == 1 }
    c
  end

  def test_the_stream_opens_before_the_first_fetch
    c = connected_client
    stream = @server.stream_requests.first
    assert_operator stream.at, :<, @server.fetches.first.at
    assert_equal "text/event-stream", stream["accept"]
    assert_equal "Bearer fn_srv_#{"a" * 40}", stream["authorization"]
    assert_equal "flagnetic-ruby/#{Flagnetic::VERSION} (server)", stream["user-agent"]
    assert_nil stream["last-event-id"], "nothing held yet"
    assert_equal 1, logged(:sse_connected).size
    assert c.bool("a", default: false)
  end

  def test_an_event_reaches_on_change_with_the_exact_keys_within_two_seconds
    c = connected_client
    changes = Queue.new
    c.on_change { |keys| changes << [keys, now] }

    published = now
    @server.publish(document(2, { "a" => true, "b" => "y" }))
    keys, at = pop(changes, 2)
    assert_equal ["b"], keys
    elapsed = at - published
    assert_operator elapsed, :<, 2
    assert_equal "y", c.string("b", default: "")
    assert_equal '"1"', @server.fetches.last["if-none-match"]
  end

  def test_an_event_at_or_below_the_held_version_fetches_nothing
    connected_client
    fetches = @server.fetches.size
    @server.emit(1)
    @server.emit(0)
    sleep 0.3
    assert_equal fetches, @server.fetches.size
  end

  def test_a_304_fires_nothing
    c = connected_client
    changes = Queue.new
    c.on_change { |keys| changes << keys }
    @server.close_streams # reconnect, then a fetch answered 304
    eventually { @server.fetches.size >= 2 }
    assert_nil pop(changes, 0.3)
  end

  def test_after_a_server_side_close_it_reconnects_with_last_event_id_and_catches_up
    c = connected_client
    @server.close_streams
    @server.publish(document(3, { "a" => false, "b" => "x" }), quiet: true) # bumped during the gap

    eventually { @server.stream_requests.size == 2 }
    assert_equal "1", @server.stream_requests.last["last-event-id"]
    eventually { !c.bool("a", default: true) }
    refute c.bool("a", default: true)
    assert_equal [[:info, { cause: "closed by the server" }]], logged(:sse_dropped)
    assert_equal 2, logged(:sse_connected).size
  end

  def test_after_a_broken_stream_it_reconnects_with_backoff_and_fetches
    connected_client
    fetches = @server.fetches.size
    @server.reset_streams
    eventually { @server.stream_requests.size == 2 }
    eventually { @server.fetches.size > fetches }
    level, fields = logged(:sse_dropped).first
    assert_equal :warn, level
    refute_equal "closed by the server", fields[:cause]
  end

  def test_silence_reconnects
    connected_client(timing: { silence: 0.3 })
    eventually { @server.stream_requests.size >= 2 }
    assert_match(/no frame for 0.3 s/, logged(:sse_dropped).first[1][:cause])
  end

  def test_heartbeats_keep_the_stream_alive
    connected_client(timing: { silence: 0.3 })
    5.times do
      sleep 0.1
      @server.heartbeat
    end
    assert_equal 1, @server.stream_requests.size
  end

  def test_a_fetch_below_the_events_version_is_retried_after_one_two_and_four_seconds
    c = connected_client(timing: { event_retries: [0.2, 0.5, 1.0] })
    behind = 0
    @server.on_ruleset do |_|
      next nil if behind >= 2

      behind += 1
      [304, {}, ""] # a replica still behind the event
    end
    before = @server.fetches.size
    @server.publish(document(2, { "a" => false, "b" => "x" }))
    eventually { !c.bool("a", default: true) }

    retries = @server.fetches.drop(before)
    assert_equal 3, retries.size
    gaps = retries.each_cons(2).map { |a, b| b.at - a.at }
    assert_operator gaps[0], :>=, 0.2
    assert_operator gaps[0], :<, 0.2 + 0.2
    assert_operator gaps[1], :>=, 0.5
    assert_operator gaps[1], :<, 0.5 + 0.2
  end

  def test_the_held_version_never_goes_down
    c = connected_client
    @server.on_ruleset { |_| [200, {}, JSON.generate(document(0, { "a" => false }))] }
    @server.emit(5)
    eventually { logged(:fetch_failed).size >= 3 }
    d = c.detail("a", default: false)
    assert_equal [true, 1], [d.value, d.ruleset_version]
    assert_match(/below the held 1/, logged(:fetch_failed).first[1][:cause])
  end

  def test_a_revoked_key_on_the_stream_closes_it_and_logs_once
    @server.on_stream { |_| [401, {}, ""] }
    @server.on_ruleset { |_| [401, {}, ""] }
    c = client(timing: { poll_down: 0.05 })
    assert_equal :built_in, c.ready(timeout: 2)
    eventually { @server.fetches.size >= 4 }
    assert_empty logged(:sse_connected)
    assert_equal [[:error, { status: 401, source: "stream" }], [:error, { status: 401, source: "fetch" }]],
                 logged(:key_revoked), "once per source"
    assert_empty logged(:sse_dropped)
  end

  def test_a_refused_stream_is_retried_and_a_restored_key_gets_it_back
    @server.publish(document(1, { "a" => true }))
    refused = true
    @server.on_stream { |_| refused ? [403, {}, ""] : nil }
    c = client
    assert_equal :fresh, c.ready(timeout: 2)
    eventually { @server.stream_requests.size >= 2 }
    refused = false
    eventually { @server.streams.size == 1 }
    assert_equal 1, @server.streams.size
    assert_equal 1, logged(:key_revoked).size
    assert c.bool("a", default: false)
  end

  def test_a_revoked_fetch_closes_an_open_stream_and_recovery_reopens_it
    c = connected_client(timing: { poll_connected: 0.05 })
    revoked = true
    @server.on_ruleset { |_| revoked ? [403, {}, ""] : nil }
    eventually { @server.streams.empty? && logged(:key_revoked).any? }
    assert_equal :last_good, c.stage
    streams = @server.stream_requests.size

    revoked = false
    eventually { @server.stream_requests.size > streams }
    assert_equal :fresh, c.stage
  end

  def test_the_stream_can_be_disabled
    @server.publish(document(1, { "a" => true }))
    c = client(stream: false, timing: { poll_down: 0.05 })
    assert_equal :fresh, c.ready(timeout: 2)
    eventually { @server.fetches.size >= 3 }
    assert_empty @server.stream_requests
  end

  def test_close_returns_promptly_while_connected_and_during_backoff
    c = connected_client
    started = now
    c.close(timeout: 5)
    assert_operator now - started, :<, 0.5

    @server.on_ruleset { |_| [500, {}, ""] }
    fetches = @server.fetches.size
    backing_off = client(stream: false, timing: { fetch_base: 30, fetch_cap: 30 })
    eventually { @server.fetches.size > fetches }
    sleep 0.1
    started = now
    backing_off.close(timeout: 5)
    assert_operator now - started, :<, 0.5
    fetches = @server.fetches.size
    sleep 0.2
    assert_equal fetches, @server.fetches.size, "nothing runs after close"
    assert backing_off.bool("missing", default: true), "getters keep serving"
  end
end
