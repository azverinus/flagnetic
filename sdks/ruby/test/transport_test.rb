# frozen_string_literal: true

require "test_helper"

# A response body is read in pieces and counted, so a peer cannot make the
# SDK hold an unbounded answer in memory, gzip-compressed or not.
class TransportTest < Minitest::Test
  include ClientHelpers

  Transport = Flagnetic.const_get(:Transport)

  def transport(**)
    Transport.new(sdk_key: "fn_srv_x", base_url: @server.base_url, platform: "server", timeout: 2, **)
  end

  def test_the_default_limits
    assert_equal 256 << 20, Transport::RULESET_LIMIT
    assert_equal 1 << 20, Transport::ANSWER_LIMIT
  end

  def test_a_ruleset_at_the_limit_is_read
    @server.on_ruleset { |_| [200, {}, "x" * 100] }
    response = transport(ruleset_limit: 100).fetch_ruleset(nil)
    assert_equal ["x" * 100, 200], [response.body, response.status]
  end

  def test_a_ruleset_over_the_limit_is_a_failed_fetch
    @server.on_ruleset { |_| [200, {}, "x" * 101] }
    error = assert_raises(Transport::BodyTooLarge) { transport(ruleset_limit: 100).fetch_ruleset(nil) }
    assert_match(/exceeds 100 bytes/, error.message)
  end

  def test_a_gzipped_ruleset_is_bounded_by_its_decompressed_size
    @server.gzip!
    @server.on_ruleset { |_| [200, {}, "x" * 100_000] } # compresses to a few hundred bytes
    assert_raises(Transport::BodyTooLarge) { transport(ruleset_limit: 1000).fetch_ruleset(nil) }
    assert_equal 100_000, transport(ruleset_limit: 100_000).fetch_ruleset(nil).body.bytesize
  end

  def test_the_breach_is_an_error_the_fetch_path_rescues
    assert_operator Transport::BodyTooLarge, :<, StandardError
  end

  def test_an_events_answer_over_the_limit_is_cut_off_without_failing_the_post
    @server.on_events { |_| [202, {}, "x" * 2000] }
    response = transport(answer_limit: 1000).post_events("gz")
    assert_equal [202, ""], [response.status, response.body]
  end

  def test_an_events_answer_at_the_limit_is_read
    @server.on_events { |_| [202, {}, "x" * 1000] }
    assert_equal 1000, transport(answer_limit: 1000).post_events("gz").body.bytesize
  end
end
