# frozen_string_literal: true

$LOAD_PATH.unshift(File.expand_path("../lib", __dir__))

require "json"
require "net/http"
require "open3"
require "time"
require "minitest/autorun"
require "flagnetic"

# The smoke every SDK runs against two real replicas on one PostgreSQL,
# brought up and seeded by `flagnetic-e2e up` (e2e/README.md). It reads the
# seed from FLAGNETIC_E2E_SEED and runs the control commands through
# FLAGNETIC_E2E_COMMAND (default: flagnetic-e2e on the PATH). Run it with
# `bundle exec rake e2e`.
class SmokeTest < Minitest::Test
  SEED_PATH = ENV.fetch("FLAGNETIC_E2E_SEED") { abort "FLAGNETIC_E2E_SEED must name the seed `flagnetic-e2e up` wrote" }
  SEED = JSON.parse(File.read(SEED_PATH))
  COMMAND = ENV.fetch("FLAGNETIC_E2E_COMMAND", "flagnetic-e2e")

  # The kill switch changes the seed until it is restored, and the
  # revocation ends the server key, so the tests run in the order of their
  # names.
  def self.test_order = :alpha

  def teardown
    @clients&.each { |c| c.close(timeout: 2) }
    super
  end

  def test_1_ready_reaches_fresh_and_every_case_serves_its_true_reason
    c = client(SEED["replicas"]["a"])
    assert_equal :fresh, c.ready(timeout: 10)

    SEED["cases"].each do |k|
      d = c.detail(k["flag"], default: default_for(k), context: k["context"])
      assert_equal [k["value"], k["variation"], k["reason"]], [d.value, d.variation, d.reason.to_s], k["name"]
    end
  end

  # The segment is private: only a server key learns its members, and the
  # SDK decides membership itself from the ruleset it holds.
  def test_2_the_private_segment_decides_the_value
    c = client(SEED["replicas"]["b"])
    assert_equal :fresh, c.ready(timeout: 10)
    flag = SEED["flags"]["private_segment"]
    member = c.detail(flag, default: false, context: unit(SEED["units"]["segment_member"]))
    assert_equal [true, :rule], [member.value, member.reason]
    other = c.detail(flag, default: true, context: unit(SEED["units"]["outside_rollout"]))
    assert_equal [false, :fallthrough], [other.value, other.reason]
  end

  def test_3_a_kill_switch_through_replica_a_reaches_the_sdk_streaming_from_replica_b_within_two_seconds
    flag = SEED["flags"]["on"]
    c = client(SEED["replicas"]["b"], poll_interval: 3600)
    assert_equal :fresh, c.ready(timeout: 10)
    assert wait_until(5) { logged?(:sse_connected) }, "the stream never connected"
    # The replica announces the version the client holds as the stream
    # opens; give it a moment so that the kill switch is news to it.
    sleep 0.5
    changed = Queue.new
    c.on_change { |keys| changed << Time.now if keys.include?(flag) }

    begin
      killed = control("kill", flag)
      assert killed["changed"], "the kill switch changed nothing"
      at = changed.pop(timeout: 10)
      refute_nil at, "the kill switch never reached the SDK"
      took = ((at - Time.iso8601(killed["committed_at"])) * 1000).round
      report(took)
      assert_operator took, :<=, 2000, "the kill switch took #{took} ms to reach the SDK"
      d = c.detail(flag, default: true, context: unit(SEED["units"]["outside_rollout"]))
      assert_equal [false, :off], [d.value, d.reason]
    ensure
      control("restore")
    end
  end

  # Each replica writes the SDKs it saw once a minute.
  def test_4_the_sdk_is_in_the_inventory_under_its_own_name
    c = client(SEED["replicas"]["b"])
    c.ready(timeout: 10)
    listed = wait_until(90, every: 2) do
      control("inventory").any? do |entry|
        entry["sdk_name"] == "flagnetic-ruby" && entry["sdk_version"] == Flagnetic::VERSION &&
          entry.dig("sdk_key", "kind") == "server"
      end
    end
    assert listed, "no flagnetic-ruby #{Flagnetic::VERSION} in the inventory within 90 s"
  end

  # A replica checks the key on every request; the revocation goes through
  # replica A and is refused by replica B.
  def test_5_a_revoked_key_is_refused
    control("revoke", "server")
    refused = wait_until(5, every: 0.1) { ruleset_status(SEED["replicas"]["b"]) == "401" }
    assert refused, "replica B still serves the revoked key"
    c = client(SEED["replicas"]["b"])
    assert_equal :built_in, c.ready(timeout: 5)
    assert logged?(:key_revoked), "the refusal was not logged as key_revoked"
  end

  private

  def client(base_url, **options)
    @logged = Queue.new
    @seen = []
    @clients ||= []
    logger = ->(level, event, fields) { @logged << [level, event, fields] }
    Flagnetic::Client.new(SEED["server_key"], base_url: base_url, logger: logger, **options).tap { |c| @clients << c }
  end

  def ruleset_status(base_url)
    uri = URI("#{base_url}#{Flagnetic::SDK_API_PATH}/ruleset")
    Net::HTTP.get_response(uri, "Authorization" => "Bearer #{SEED["server_key"]}").code
  end

  def unit(id) = { units: { SEED["unit_type"] => id } }

  # default_for is a built-in default of the case's type that differs from
  # what the case serves, so that a fallback cannot pass for the value.
  def default_for(kase) = kase["type"] == "boolean" ? !kase["value"] : ""

  # control runs a control command of the harness and returns its JSON
  # output, nil when it printed nothing.
  def control(*args)
    out, err, status = Open3.capture3(COMMAND, *args)
    assert status.success?, "flagnetic-e2e #{args.join(" ")}: #{err}"
    out.strip.empty? ? nil : JSON.parse(out)
  end

  # report prints the kill switch's propagation as one JSON line, and adds
  # it to the CI job's summary when there is one.
  def report(milliseconds)
    line = JSON.generate(sdk: "flagnetic-ruby", kill_switch_ms: milliseconds)
    puts "\n#{line}"
    summary = ENV.fetch("GITHUB_STEP_SUMMARY", "")
    File.write(summary, "#{line}\n", mode: "a") unless summary.empty?
  end

  def now = Process.clock_gettime(Process::CLOCK_MONOTONIC)

  def wait_until(seconds, every: 0.05)
    deadline = now + seconds
    until (result = yield)
      break if now > deadline

      sleep every
    end
    result
  end

  def logged?(event)
    @seen << @logged.pop until @logged.empty?
    @seen.any? { |_, e, _| e == event }
  end
end
