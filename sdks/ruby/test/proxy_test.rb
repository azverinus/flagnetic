# frozen_string_literal: true

require "test_helper"

# The fetch and the change stream both go through the HTTP proxy the process
# is configured with. The mock server plays the proxy: it takes the
# absolute-form request a proxy receives and answers as the origin would.
class ProxyTest < Minitest::Test
  include ClientHelpers

  ORIGIN = "flags.example.test:8443"

  def around_proxy
    saved = ENV.to_h.slice("http_proxy", "HTTP_PROXY", "no_proxy", "NO_PROXY")
    %w[HTTP_PROXY no_proxy NO_PROXY].each { |k| ENV.delete(k) }
    ENV["http_proxy"] = @server.base_url
    yield
  ensure
    %w[http_proxy HTTP_PROXY no_proxy NO_PROXY].each { |k| ENV.delete(k) }
    saved.each { |k, v| ENV[k] = v }
  end

  def test_the_fetch_and_the_stream_go_through_the_configured_proxy
    @server.publish(document(1, { "on" => true }))
    around_proxy do
      c = Flagnetic::Client.new("fn_srv_#{"a" * 40}", base_url: "http://#{ORIGIN}", logger: method(:record),
                                                      timing: FAST)
      @clients << c
      assert_equal :fresh, c.ready(timeout: 2)
      eventually { @server.streams.size == 1 }
      assert c.bool("on", default: false)

      @server.publish(document(2, { "on" => false }))
      eventually { !c.bool("on", default: true) }
      refute c.bool("on", default: true)
    end
    assert_equal [ORIGIN], (@server.fetches + @server.stream_requests).map { |r| r["host"] }.uniq
  end
end
