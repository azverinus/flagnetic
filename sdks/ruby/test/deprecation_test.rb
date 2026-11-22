# frozen_string_literal: true

require "test_helper"

# A Deprecation header the server answers is logged once per operation at
# warning level, with the Sunset when there is one, and never fails a call.
class DeprecationTest < Minitest::Test
  include ClientHelpers

  DEPRECATED = { "Deprecation" => "@1767225600", "Sunset" => "Wed, 01 Jul 2027 00:00:00 GMT" }.freeze

  def test_a_deprecated_ruleset_fetch_is_logged_once_with_its_sunset_and_still_loads
    document = JSON.generate(document(1, { "on" => true }))
    @server.on_ruleset { |_| [200, DEPRECATED.merge("ETag" => '"1"'), document] }
    c = client(stream: false, timing: { poll_down: 0.05 })

    assert_equal :fresh, c.ready(timeout: 2)
    assert c.bool("on", default: false)
    eventually { @server.fetches.size >= 3 }

    assert_equal [[:warn, { operation: "ruleset", deprecation: "@1767225600",
                            sunset: "Wed, 01 Jul 2027 00:00:00 GMT" }]], logged(:operation_deprecated)
  end

  def test_a_deprecation_without_a_sunset_names_none
    @server.publish(document(1, { "on" => true }))
    @server.on_ruleset { |_| [304, { "Deprecation" => "@1767225600" }, ""] }
    c = client(stream: false, timing: { poll_down: 0.05 })
    c.ready(timeout: 2)
    eventually { logged(:operation_deprecated).any? }

    assert_equal [[:warn, { operation: "ruleset", deprecation: "@1767225600" }]],
                 logged(:operation_deprecated)
  end

  def test_a_deprecated_events_operation_is_logged_once_and_the_batch_is_still_accepted
    @server.publish(document(1, { "on" => true }))
    @server.on_events do |request|
      [202, DEPRECATED, JSON.generate("accepted" => request.events.size, "duplicates" => 0, "dropped_rows" => 0,
                                      "rejected" => [])]
    end
    c = client(stream: false)
    c.ready(timeout: 2)
    2.times do
      c.alias("anonymous", "d-1", "user", "u-1")
      c.flush(timeout: 2)
    end

    assert_equal 2, @server.events_requests.size
    assert_equal(["events"], logged(:operation_deprecated).map { |_, f| f[:operation] })
    assert_empty logged(:events_failed)
  end

  def test_a_deprecated_stream_is_logged_once_and_still_streams
    @server.publish(document(1, { "on" => true }))
    @server.on_stream { |_| [200, DEPRECATED.merge("Content-Type" => "text/event-stream"), ": hb\n\n"] }
    c = client(timing: { stream_base: 0.02, stream_cap: 0.05 })
    c.ready(timeout: 2)
    eventually { @server.stream_requests.size >= 3 }

    assert_equal(["stream"], logged(:operation_deprecated).map { |_, f| f[:operation] })
  end

  def test_a_header_that_does_not_parse_is_still_logged_and_fails_nothing
    document = JSON.generate(document(1, { "on" => true }))
    @server.on_ruleset { |_| [200, { "Deprecation" => "soon", "ETag" => '"1"' }, document] }
    c = client(stream: false)

    assert_equal :fresh, c.ready(timeout: 2)
    assert_equal [[:warn, { operation: "ruleset", deprecation: "soon" }]], logged(:operation_deprecated)
  end

  def test_an_answer_without_the_header_logs_nothing
    @server.publish(document(1, { "on" => true }))
    c = client(stream: false)
    c.ready(timeout: 2)

    assert_empty logged(:operation_deprecated)
  end

  def test_a_logger_that_raises_does_not_fail_the_call
    @server.publish(document(1, { "on" => true }))
    @server.on_ruleset { |_| [200, DEPRECATED.merge("ETag" => '"1"'), JSON.generate(document(1, { "on" => true }))] }
    boom = ->(_level, event, _fields) { raise "boom" if event == :operation_deprecated }
    c = Flagnetic::Client.new(KEY, base_url: @server.base_url, stream: false, timing: FAST, logger: boom)
    @clients << c

    assert_equal :fresh, c.ready(timeout: 2)
    assert c.bool("on", default: false)
  end
end
