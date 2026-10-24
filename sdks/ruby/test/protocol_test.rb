# frozen_string_literal: true

require "test_helper"
require "tmpdir"
require_relative "support/scenario_server"

# The protocol fixtures in conformance/protocol: the degradation cases of
# the ruleset endpoint and the server-mode scenarios of delivery, run
# against the client on real time with every duration divided by a factor,
# as the fixtures' README allows. A scenario that sends events, or records
# an exposure or an alias, belongs to the events runner (see
# reports_events?).
class ProtocolTest < Minitest::Test
  include Eventually

  DIR = File.expand_path("../../../conformance/protocol", __dir__)

  def self.read(name) = File.binread(File.join(DIR, name))
  def read(name) = self.class.read(name)

  # scenarios.json, with what only the SDKs decide (versioning and
  # compatibility) appended: the same step language over the same documents.
  SCENARIOS = JSON.parse(read("scenarios.json")).tap do |list|
    list["scenarios"] += JSON.parse(read("versioning-scenarios.json"))["scenarios"]
  end
  CASES = JSON.parse(read("degradation/cases.json"))
  SERVER_KEY = "fn_srv_#{"p" * 40}".freeze

  # FACTOR divides every duration of a scenario; the revoked key's
  # 15-minute cap needs a larger one. SLACK is the test process's own
  # scheduling latency, in real seconds, allowed past a window's end.
  FACTOR = 50.0
  FACTORS = { "server: a revoked key keeps the last good and retries at a capped backoff" => 2000.0 }.freeze
  SLACK = 0.3

  def setup
    super
    @server = ScenarioServer.new
    @log_mutex = Mutex.new
    @logged = Hash.new(0)
  end

  def teardown
    @client&.close(timeout: 1)
    @server.close
    super
  end

  def now = Process.clock_gettime(Process::CLOCK_MONOTONIC)

  def record(_level, event, _fields)
    @log_mutex.synchronize { @logged[event.to_s] += 1 }
  end

  def build(factor, options, stored: nil, offline: nil)
    timing = Flagnetic::Client::DEFAULT_TIMING.to_h.transform_values do |v|
      v.is_a?(Array) ? v.map { |x| x / factor } : v / factor
    end
    poll = options["poll_interval_ms"] / 1000.0 / factor
    timing.merge!(request_timeout: 2, poll_connected: poll, poll_down: poll)
    common = { allow_overrides: options["allow_overrides"], timing: timing, logger: method(:record) }
    if offline
      @client = Flagnetic::Client.new(offline: read(offline), **common)
      return
    end
    store = nil
    if stored
      store = File.join(Dir.mktmpdir, "ruleset.json")
      File.binwrite(store, read(stored))
    end
    @client = Flagnetic::Client.new(SERVER_KEY, base_url: @server.base_url, stream: options["sse"], store: store,
                                                **common)
  end

  # --- degradation cases --------------------------------------------------

  def test_every_ruleset_degradation_case
    cases = CASES["cases"].select { |c| c["endpoint"] == "GET /sdk/v1/ruleset" }
    refute_empty cases
    cases.each do |c|
      @log_mutex.synchronize { @logged.clear }
      server = ScenarioServer.new
      client = Flagnetic::Client.new(SERVER_KEY, base_url: server.base_url, stream: false, logger: method(:record),
                                                 timing: { poll_connected: 0.02, poll_down: 0.02 })
      held = server.take("/sdk/v1/ruleset", 2)
      server.respond(held, { "status" => 200, "headers" => { "Content-Type" => "application/json" } }, read(c["held"]))
      assert_equal :fresh, client.ready(timeout: 2), c["name"]
      response = server.take("/sdk/v1/ruleset", 2)
      server.respond(response, c["response"], c["response"]["body"] ? read(c["response"]["body"]) : "")

      want = c["outcome"] == "accept" ? :fresh : :last_good
      eventually(1) do
        client.stage == want && (want == :fresh || client.status.stale_since) && answers?(client, c["answers"])
      end
      assert_equal want, client.stage, c["name"]
      assert_answers client, c["answers"], c["name"]
      (c["logs"] || {}).each do |event, n|
        assert_equal n, @log_mutex.synchronize { @logged[event] }, "#{c["name"]}: #{event} logged"
      end
    ensure
      client&.close(timeout: 1)
      server&.close
    end
  end

  # --- scenarios ----------------------------------------------------------

  EVENT_CALLS = %w[exposure alias flush].freeze

  # reports_events? tells whether a scenario needs events: it expects a
  # request to the events endpoint or calls an API that records or sends
  # one.
  def self.reports_events?(scenario)
    scenario["steps"].any? do |step|
      step.dig("request", "path") == "/sdk/v1/events" || EVENT_CALLS.include?(step.dig("call", "op"))
    end
  end

  SCENARIOS["scenarios"].select { |s| s["mode"] == "server" && !reports_events?(s) }.each_with_index do |scenario, i|
    define_method(:"test_scenario_#{i}_#{scenario["name"].delete_prefix("server: ").gsub(/\W+/, "_")}") do
      run_scenario(scenario)
    end
  end

  def test_every_delivery_scenario_talks_to_the_ruleset_and_stream_endpoints_only
    refute_empty delivery_scenarios
    paths = delivery_scenarios.flat_map { |s| s["steps"].filter_map { |st| st.dig("request", "path") } }.uniq
    assert_empty paths - ["/sdk/v1/ruleset", "/sdk/v1/stream"]
  end

  def delivery_scenarios
    SCENARIOS["scenarios"].select { |s| s["mode"] == "server" && !self.class.reports_events?(s) }
  end

  def run_scenario(scenario)
    factor = FACTORS.fetch(scenario["name"], FACTOR)
    given = scenario["given"]
    build(factor, given["options"], stored: given["stored"], offline: given["offline"])
    step_end = now
    scenario["steps"].each_with_index do |step, i|
      label = "#{scenario["name"]}, step #{i}#{" (#{step["note"]})" if step["note"]}"
      results = act(step, label, factor, step_end)
      step_end = now
      check(step["expect"], results, label) if step["expect"]
    end
    # What the SDK sends later (a retry the last step scheduled) is outside
    # the scenario; every request before its end must have had a step.
    left = @server.pending.select { |r| r.at <= step_end }
    assert_empty left.map { |r| "#{r.verb} #{r.path}" }, "#{scenario["name"]}: unexpected requests"
  end

  # act runs a step's action and returns the results of its calls.
  def act(step, label, factor, step_end)
    if (request = step["request"]) then answer(request, step["respond"], label, factor, step_end)
    elsif step.key?("stream_send") then @server.stream_send(step["stream_send"])
    elsif step["stream_close"] then @server.stream_close
    elsif step["advance_ms"] then sleep(step["advance_ms"] / 1000.0 / factor)
    elsif (quiet = step["no_request"])
      sleep(quiet["for_ms"] / 1000.0 / factor)
      assert_empty @server.pending(quiet["path"]).map(&:path), "#{label}: a request during #{quiet["for_ms"]} ms"
    elsif (call = step["call"]) then return call(call)
    end
    nil
  end

  def answer(expected, respond, label, factor, step_end)
    window = expected["delay_ms"]
    timeout = window ? (window["max"] / 1000.0 / factor) + SLACK - (now - step_end) : 3
    request = @server.take(expected["path"], [timeout, 0].max)
    refute_nil request, "#{label}: no #{expected["method"]} #{expected["path"]} within its window"
    assert_operator request.at - step_end, :>=, (window["min"] / 1000.0 / factor) - 0.003, label if window
    assert_equal expected["method"], request.verb, label
    (expected["headers"] || {}).each do |name, value|
      assert_equal value.sub("<sdk key>", SERVER_KEY), request[name], "#{label}: #{name}"
    end
    (expected["absent_headers"] || []).each { |name| assert_nil request[name], "#{label}: #{name} sent" }
    @server.respond(request, respond, respond["body"] ? read(respond["body"]) : "")
  end

  def call(call)
    case call["op"]
    when "evaluate"
      Array.new(call.fetch("times", 1)) do
        @client.detail(call["flag"], default: call["default"], context: SCENARIOS["context"])
      end
    when "set_override"
      begin
        @client.local_overrides.set(call["flag"], call["value"])
        [false]
      rescue Flagnetic::Error
        [true]
      end
    else flunk "unknown call #{call["op"]}"
    end
  end

  # check waits briefly for the SDK to apply the step's response, then
  # asserts every expectation.
  def check(expect, results, label)
    eventually(1) { holds?(expect) }
    assert_equal expect["stage"].to_sym, @client.stage, "#{label}: stage" if expect["stage"]
    case expect["stale_since"]
    when "set" then refute_nil @client.status.stale_since, "#{label}: stale_since"
    when "unset" then assert_nil @client.status.stale_since, "#{label}: stale_since"
    end
    assert_answers @client, SCENARIOS["answers"].fetch(expect["answers_of"]), label if expect["answers_of"]
    (expect["logs"] || {}).each do |event, n|
      assert_equal n, @log_mutex.synchronize { @logged[event] }, "#{label}: #{event} logged"
    end
    assert_equal [expect["refused"]], results, "#{label}: refused" if expect.key?("refused")
    results&.each { |d| assert_answer d, expect["result"], label } if expect["result"]
  end

  # holds? reports whether the SDK's state already meets expect, so that
  # check asserts once the step's response has been applied.
  def holds?(expect)
    status = @client.status
    (expect["stage"].nil? || status.stage.to_s == expect["stage"]) &&
      (expect["stale_since"].nil? || (expect["stale_since"] == "set") == !status.stale_since.nil?) &&
      (expect["answers_of"].nil? || answers?(@client, SCENARIOS["answers"].fetch(expect["answers_of"]))) &&
      (expect["logs"] || {}).all? { |event, n| @log_mutex.synchronize { @logged[event] } >= n }
  end

  def answers?(client, answers)
    answers.all? do |a|
      d = client.detail(a["flag"], default: a["default"], context: SCENARIOS["context"])
      d.value == a["value"] && d.variation == a["variation"] && d.reason.to_s == a["reason"]
    end
  end

  def assert_answers(client, answers, label)
    answers.each do |a|
      assert_answer client.detail(a["flag"], default: a["default"], context: SCENARIOS["context"]), a, label
    end
  end

  def assert_answer(detail, want, label)
    msg = "#{label}: #{want["flag"]}"
    assert_equal want["value"], detail.value, "#{msg} value"
    assert_equal want["variation"], detail.variation, "#{msg} variation" if want.key?("variation")
    assert_nil detail.variation, "#{msg} variation" unless want.key?("variation")
    assert_equal want["reason"], detail.reason.to_s, "#{msg} reason"
  end

  # --- documents ----------------------------------------------------------

  def test_offline_mode_serves_the_ruleset_endpoints_document_as_it_is
    client = Flagnetic::Client.new(offline: read("server/ruleset.json"), logger: ->(*) {})
    assert_equal :fresh, client.stage
    assert_answers client, SCENARIOS["answers"]["server/ruleset.json"], "offline"
  end

  def test_the_stream_transcripts_parse
    frames = parse(read("server/stream.txt"))
    assert_equal [:retry, 3000], frames.first
    events = frames.filter_map { |kind, value| value if kind == :event }
    refute_empty events
    events.each do |e|
      assert_equal "ruleset", e.type
      assert_equal Integer(e.id), JSON.parse(e.data)["version"]
    end
    assert_includes frames, [:comment, " hb"]
    assert(parse(read("server/stream-caught-up.txt")).none? { |kind, _| kind == :event })
  end

  def parse(text)
    frames = []
    Flagnetic::SSEParser.new.feed(text) { |kind, value| frames << [kind, value] }
    frames
  end
end
