# frozen_string_literal: true

$LOAD_PATH.unshift(File.expand_path("../lib", __dir__))

require "json"
require "minitest/autorun"
require "flagnetic"

module RulesetHelper
  # ruleset decodes a server document with the given unit types, attributes
  # and flags, the way most tests need one.
  def ruleset(flags:, attributes: [], unit_types: ["user"], segments: [])
    Flagnetic::Eval::Ruleset.decode(JSON.generate(
                                      "schema_version" => 1, "version" => 1, "scope" => "server",
                                      "unit_types" => unit_types, "attributes" => attributes,
                                      "segments" => segments, "flags" => flags
                                    ))
  end

  # flag is a boolean flag serving "true" when its one rule's conditions
  # hold and "false" otherwise.
  def flag(key, conditions, serve: { "kind" => "variation", "variation" => "true" })
    {
      "key" => key, "type" => "boolean", "on" => true,
      "variations" => [{ "key" => "true", "value" => true }, { "key" => "false", "value" => false }],
      "off_variation" => "false", "fallthrough" => "false",
      "rules" => [{ "id" => "r1", "conditions" => conditions, "serve" => serve }]
    }
  end
end

# RegexHelper puts a pattern and an input through the public seam, a
# ruleset: true when the rule matched, false at the fallthrough, "invalid"
# for inconsistent_ruleset.
module RegexHelper
  extend RulesetHelper

  def self.matches(pattern, input)
    rs = ruleset(attributes: [{ "key" => "s", "type" => "string" }],
                 flags: [flag("f",
                              [{ "kind" => "attribute", "attribute" => "s", "op" => "matches", "value" => pattern }])])
    r = rs.evaluate("f", { attributes: { s: input } })
    return "invalid" if r.error_code == :inconsistent_ruleset

    r.reason == :rule
  end
end

require_relative "support/mock_server"

module Eventually
  # eventually retries the block until it returns a truthy value or
  # timeout seconds pass, and returns its last value.
  def eventually(timeout = 5)
    deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
    loop do
      result = yield
      return result if result || Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline

      sleep 0.01
    end
  end
end

# ClientHelpers runs clients against a MockServer with timings scaled down
# from seconds to tens of milliseconds, and captures their log events.
module ClientHelpers
  include Eventually

  FAST = {
    request_timeout: 2, fetch_base: 0.05, fetch_cap: 0.2, unauthorized_cap: 0.4, stream_base: 0.05, stream_cap: 0.2,
    stream_stable: 60, close_jitter: 0.05, silence: 45, poll_connected: 300, poll_down: 300,
    event_retries: [0.1, 0.2, 0.4]
  }.freeze

  def setup
    super
    @server = MockServer.new
    @clients = []
    @log_mutex = Mutex.new
    @logged = []
  end

  def teardown
    @clients.each { |c| c.close(timeout: 1) }
    @server.close
    super
  end

  def client(timing: {}, **)
    c = Flagnetic::Client.new("fn_srv_#{"a" * 40}", base_url: @server.base_url, logger: method(:record),
                                                    timing: FAST.merge(timing), **)
    @clients << c
    c
  end

  def record(level, event, fields)
    @log_mutex.synchronize { @logged << [level, event, fields] }
  end

  # logged returns the [level, fields] of every event with that name.
  def logged(event)
    @log_mutex.synchronize { @logged.select { |e| e[1] == event }.map { |l, _, f| [l, f] } }
  end

  # document builds a server ruleset serving each value to everyone as the
  # fallthrough of a flag with one variation.
  def document(version, values = {}, protected: false, **extra)
    flags = values.map do |key, value|
      type = Flagnetic::Client.__send__(:type_of, value)
      { "key" => key.to_s, "type" => type, "on" => true, "variations" => [{ "key" => "v", "value" => value }],
        "off_variation" => "v", "fallthrough" => "v" }
    end
    { "schema_version" => 1, "version" => version, "scope" => "server", "flags" => flags,
      "environment" => { "key" => "development", "protected" => protected } }.merge(extra.transform_keys(&:to_s))
  end

  # pop takes the next item of queue, or nil after timeout seconds.
  def pop(queue, timeout)
    queue.pop(timeout: timeout)
  end
end
