# frozen_string_literal: true

require "test_helper"
require_relative "../../ruby/test/support/mock_server"

# EventsTest covers the provider events: CONFIGURATION_CHANGED with the
# changed keys, STALE when the client falls back to its last good ruleset,
# READY when it is fresh again, and ERROR on a revoked SDK key, against a
# real HTTP server for the transitions only a network can cause.
class EventsTest < Minitest::Test
  include ProviderHelper

  # The client's delays scaled down from seconds to tens of milliseconds;
  # timing is the core client's option for its own tests.
  FAST = {
    request_timeout: 2, fetch_base: 0.05, fetch_cap: 0.2, unauthorized_cap: 0.2, stream_base: 0.05, stream_cap: 0.2,
    stream_stable: 60, close_jitter: 0.05, silence: 45, poll_connected: 300, poll_down: 0.05,
    event_retries: [0.1, 0.2, 0.4]
  }.freeze

  def setup
    super
    @server = MockServer.new
    @events = Queue.new
    @logged = Queue.new
  end

  def teardown
    super
    @server.close
  end

  def online_provider(**)
    provider = Flagnetic::OpenFeature::Provider.new(
      "fn_srv_#{"a" * 40}", base_url: @server.base_url, stream: false, timing: FAST, ready_timeout: 2,
                            logger: ->(level, event, fields) { @logged << [level, event, fields] }, **
    )
    _, client = register(provider)
    OF::ProviderEvent::ALL_EVENTS.each do |event|
      client.add_handler(event) { |details| @events << [event, details] }
    end
    @events.clear
    [provider, client]
  end

  # next_event waits for the next event of type, skipping others.
  def next_event(type, timeout = 3)
    deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
    loop do
      left = deadline - Process.clock_gettime(Process::CLOCK_MONOTONIC)
      flunk "no #{type} within #{timeout} s" unless left.positive?
      event, details = @events.pop(timeout: left)
      return details if event == type
    end
  end

  def revoke_after_startup(**)
    @server.publish(document(1))
    revoked = false
    @server.on_ruleset { |_| revoked ? [403, {}, ""] : nil }
    online_provider(**)
    revoked = true
  end

  def document(version, greeting = "hi")
    { "schema_version" => 1, "version" => version, "scope" => "server",
      "environment" => { "key" => "development", "protected" => false },
      "flags" => [{ "key" => "greeting", "type" => "string", "on" => true,
                    "variations" => [{ "key" => "v", "value" => greeting }], "off_variation" => "v",
                    "fallthrough" => "v" }] }
  end

  def test_a_change_is_configuration_changed_with_the_changed_keys
    source = { document: JSON.generate(DOCUMENT) }
    provider, client = of_provider(offline: -> { source[:document] })
    changed = Queue.new
    client.add_handler(OF::ProviderEvent::PROVIDER_CONFIGURATION_CHANGED) { |details| changed << details }

    parsed = JSON.parse(source[:document])
    parsed["flags"].find { |f| f["key"] == "greeting" }["fallthrough"] = "hello"
    source[:document] = JSON.generate(parsed)
    provider.client.reload

    assert_equal ["greeting"], changed.pop(timeout: 2)[:flags_changed]
  end

  def test_a_failed_fetch_is_stale_and_recovery_is_ready
    @server.publish(document(1))
    failing = false
    @server.on_ruleset { |_| failing ? [503, {}, ""] : nil }
    _, client = online_provider
    assert_equal OF::ProviderState::READY, client.provider_status

    failing = true
    assert_match(/last_good/, next_event(OF::ProviderEvent::PROVIDER_STALE)[:message])
    assert_equal OF::ProviderState::STALE, client.provider_status

    failing = false
    next_event(OF::ProviderEvent::PROVIDER_READY)
    assert_equal OF::ProviderState::READY, client.provider_status
  end

  def test_a_key_revoked_while_fresh_leaves_the_provider_in_error_until_it_recovers
    @server.publish(document(1))
    revoked = false
    @server.on_ruleset { |_| revoked ? [401, {}, ""] : nil }
    _, client = online_provider

    revoked = true
    details = next_event(OF::ProviderEvent::PROVIDER_ERROR)
    assert_equal OF::Provider::ErrorCode::GENERAL, details[:error_code]
    sleep 0.3 # several refused fetches
    assert_equal OF::ProviderState::ERROR, client.provider_status
    refute(@events.size.times.map { @events.pop }.any? { |e, _| e == OF::ProviderEvent::PROVIDER_STALE },
           "no STALE while the key stays revoked")

    revoked = false
    next_event(OF::ProviderEvent::PROVIDER_READY)
    assert_equal OF::ProviderState::READY, client.provider_status
    assert_equal "hi", client.fetch_string_value(flag_key: "greeting", default_value: "x")
  end

  # openfeature-sdk 0.7 and later let init announce what it found; older
  # releases send READY themselves once init returns, so there a key
  # revoked before then reads READY until the client recovers.
  def test_a_key_revoked_at_startup_is_an_error
    @server.on_ruleset { |_| [401, {}, ""] }
    _, client = online_provider(ready_timeout: 0.5)
    lifecycle = OF::Configuration.private_method_defined?(:emits_lifecycle_events?)
    assert_equal lifecycle ? OF::ProviderState::ERROR : OF::ProviderState::READY, client.provider_status
    assert_equal true, client.fetch_boolean_value(flag_key: "greeting", default_value: true)
  end

  def test_a_failed_first_fetch_is_ready_serving_built_in_defaults
    @server.on_ruleset { |_| [503, {}, ""] }
    _, client = online_provider(ready_timeout: 0.5)
    assert_equal OF::ProviderState::READY, client.provider_status
    assert_equal "x", client.fetch_string_value(flag_key: "greeting", default_value: "x")
  end

  def test_a_revoked_key_is_still_logged_to_the_application
    revoke_after_startup
    next_event(OF::ProviderEvent::PROVIDER_ERROR)
    level, _, fields = @logged.size.times.map { @logged.pop }.find { |_, event, _| event == :key_revoked }
    assert_equal [:error, 403], [level, fields[:status]]
  end

  def test_a_revoked_key_is_an_error_even_when_the_logger_raises
    revoke_after_startup(logger: ->(*) { raise "logger down" })
    next_event(OF::ProviderEvent::PROVIDER_ERROR)
  end

  def test_initialize_waits_for_ready_and_never_fails
    @server.on_ruleset do |_|
      sleep 1
      [503, {}, ""]
    end
    started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
    provider, client = online_provider(ready_timeout: 0.3)
    assert_includes 0.3..0.9, Process.clock_gettime(Process::CLOCK_MONOTONIC) - started
    assert_equal :built_in, provider.client.stage
    assert_equal OF::ProviderState::READY, client.provider_status
  end

  def test_no_event_fires_after_shutdown
    source = { document: JSON.generate(DOCUMENT) }
    provider, client = of_provider(offline: -> { source[:document] })
    changed = Queue.new
    client.add_handler(OF::ProviderEvent::PROVIDER_CONFIGURATION_CHANGED) { |details| changed << details }
    provider.shutdown
    source[:document] = JSON.generate(DOCUMENT.merge("version" => 4, "flags" => []))
    provider.client.reload
    assert_nil changed.pop(timeout: 0.2)
  end
end
