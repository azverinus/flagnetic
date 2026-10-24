# frozen_string_literal: true

require "test_helper"
require "tmpdir"
require_relative "support/scenario_server"

# The protocol fixtures in conformance/protocol: the degradation cases of
# the ruleset endpoint and every server-mode scenario, delivery, events and
# experiments alike, run against the client on real time with every
# duration divided by a factor, as the fixtures' README allows.
class ProtocolTest < Minitest::Test
  include Eventually

  DIR = File.expand_path("../../../conformance/protocol", __dir__)

  def self.read(name) = File.binread(File.join(DIR, name))
  def read(name) = self.class.read(name)

  # scenarios.json, with what only the SDKs decide (versioning and
  # compatibility) and the running experiments appended: the same step
  # language over the same documents.
  SCENARIOS = JSON.parse(read("scenarios.json")).tap do |list|
    list["scenarios"] += JSON.parse(read("versioning-scenarios.json"))["scenarios"]
    experiments = JSON.parse(read("experiment-scenarios.json"))
    list["scenarios"] += experiments["scenarios"]
    list["answers"].merge!(experiments["answers"])
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
    @observed = Hash.new(0)
    @step_event_ids = {}
  end

  def teardown
    # The mock answers only scripted requests, so the flush close begins
    # with would wait out its whole timeout for events nobody answers.
    @client&.close(timeout: 0.2)
    @server.close
    super
  end

  def now = Process.clock_gettime(Process::CLOCK_MONOTONIC)

  def record(_level, event, fields)
    @log_mutex.synchronize do
      @logged[event.to_s] += 1
      @loaded_version = fields[:ruleset_version] if event == :ruleset_loaded
    end
  end

  def build(factor, options, stored: nil, offline: nil)
    timing = Flagnetic::Client::DEFAULT_TIMING.to_h.transform_values do |v|
      v.is_a?(Array) ? v.map { |x| x / factor } : v / factor
    end
    poll = options["poll_interval_ms"] / 1000.0 / factor
    flush = options["flush_interval_ms"] / 1000.0 / factor
    summary = options.fetch("summary_interval_ms", 3_600_000) / 1000.0 / factor
    timing.merge!(request_timeout: 2, poll_connected: poll, poll_down: poll, exposure_flush: flush,
                  summary_flush: summary)
    common = { allow_overrides: options["allow_overrides"] || false, send_summaries: options["send_summaries"],
               send_exposures: options["send_exposures"], timing: timing, logger: method(:record) }
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
      # The cases are about the ruleset alone: no events are reported.
      client = Flagnetic::Client.new(SERVER_KEY, base_url: server.base_url, stream: false, logger: method(:record),
                                                 send_summaries: false, send_exposures: false,
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

  SERVER_SCENARIOS = SCENARIOS["scenarios"].select { |s| s["mode"] == "server" }

  SERVER_SCENARIOS.each_with_index do |scenario, i|
    define_method(:"test_scenario_#{i}_#{scenario["name"].delete_prefix("server: ").gsub(/\W+/, "_")}") do
      run_scenario(scenario)
    end
  end

  def test_the_scenarios_cover_events_and_experiments
    paths = SERVER_SCENARIOS.flat_map { |s| s["steps"].filter_map { |st| st.dig("request", "path") } }.uniq
    assert_includes paths, "/sdk/v1/events"
    ops = SERVER_SCENARIOS.flat_map { |s| s["steps"].filter_map { |st| st.dig("call", "op") } }.uniq
    assert_empty %w[exposure alias flush] - ops
  end

  def run_scenario(scenario)
    factor = FACTORS.fetch(scenario["name"], FACTOR)
    given = scenario["given"]
    build(factor, given["options"], stored: given["stored"], offline: given["offline"])
    step_end = now
    scenario["steps"].each_with_index do |step, i|
      label = "#{scenario["name"]}, step #{i}#{" (#{step["note"]})" if step["note"]}"
      results = act(step, label, factor, step_end, i)
      step_end = now
      check(step["expect"], results, label) if step["expect"]
    end
    # What the SDK sends later (a retry the last step scheduled) is outside
    # the scenario; every request before its end must have had a step.
    left = @server.pending.select { |r| r.at <= step_end }
    assert_empty left.map { |r| "#{r.verb} #{r.path}" }, "#{scenario["name"]}: unexpected requests"
  end

  # act runs a step's action and returns the results of its calls.
  def act(step, label, factor, step_end, index)
    if (request = step["request"]) then answer(request, step["respond"], label, factor, step_end, index)
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

  def answer(expected, respond, label, factor, step_end, index)
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
    check_events(expected["events"], request, index, label) if expected["path"] == "/sdk/v1/events"
    @server.respond(request, respond, respond["body"] ? rejected_body(respond, request) : "")
    @answered_version = answered_version(respond) if expected["path"] == "/sdk/v1/ruleset"
  end

  # answered_version is the version of a ruleset document just answered,
  # which the next call waits to see loaded.
  def answered_version(respond)
    return unless respond["status"] == 200 && respond["body"]

    version = JSON.parse(read(respond["body"]))["version"]
    version if version.is_a?(Integer)
  rescue JSON::ParserError
    nil
  end

  # await_ruleset waits until the ruleset answered last is loaded, so that
  # a call after a step without expectations does not evaluate before the
  # client has read it. A document the client refuses is waited for in
  # vain, briefly.
  def await_ruleset
    version = @answered_version or return
    @answered_version = nil
    eventually(1) { @log_mutex.synchronize { @loaded_version } == version }
  end

  # rejected_body reads a scripted 202 body, substituting a rejected row's
  # event_id for the ID of the event actually sent at its index, since the
  # fixture's own ID was whatever the document was produced with.
  def rejected_body(respond, request)
    body = read(respond["body"])
    doc = JSON.parse(body)
    return body unless doc.is_a?(Hash) && doc["rejected"].is_a?(Array) && !doc["rejected"].empty?

    ids = request.events.map { |e| e["event_id"] }
    doc["rejected"].each { |r| r["event_id"] = ids[r["index"]] if ids[r["index"]] }
    JSON.generate(doc)
  rescue JSON::ParserError
    body
  end

  # check_events records this step's event IDs and asserts the batch: the
  # envelope, the event types in order, the evaluation summary's rows
  # (less those of the runner's own evaluations) and ruleset version, the
  # exposures' fields, strings that must appear nowhere in the body, and
  # its event IDs against another step's.
  def check_events(expected, request, index, label)
    body = request.decoded_body
    doc = JSON.parse(body)
    assert_equal 1, doc["schema_version"], "#{label}: envelope schema_version"
    assert_equal "flagnetic-ruby", doc.dig("sdk", "name"), "#{label}: envelope sdk"
    ids = doc["events"].map { |e| e["event_id"] }
    @step_event_ids[index] = ids
    return unless expected

    assert_equal expected["types"], doc["events"].map { |e| e["type"] }, "#{label}: event types" if expected["types"]
    check_summary(expected, doc["events"], label) if expected["summary_counts"]
    check_exposures(expected["exposures"], doc["events"], label) if expected["exposures"]
    (expected["absent_strings"] || []).each { |s| refute_includes body, s, "#{label}: #{s.inspect} in the body" }
    if expected["same_ids_as_step"]
      assert_equal @step_event_ids.fetch(expected["same_ids_as_step"]), ids, "#{label}: event IDs"
    end
    return unless expected["excludes_ids_of_step"]

    assert_empty ids & @step_event_ids.fetch(expected["excludes_ids_of_step"]), "#{label}: event IDs repeated"
  end

  # check_summary compares the summary's rows with the scenario's, once the
  # evaluations the runner made itself to check answers are taken out:
  # those are evaluations too, and the client counts every one.
  def check_summary(expected, events, label)
    summary = events.find { |e| e["type"] == "evaluation_summary" }
    refute_nil summary, "#{label}: no evaluation_summary event"
    rows = summary["counts"].filter_map do |row|
      key = [row["flag"], row["variation"], row["reason"]]
      mine = [@observed[key], row["count"]].min
      @observed[key] -= mine
      row.merge("count" => row["count"] - mine) if row["count"] > mine
    end
    order = ->(r) { [r["flag"], r["variation"].to_s, r["reason"]] }
    assert_equal expected["summary_counts"].sort_by(&order), rows.sort_by(&order), "#{label}: summary counts"
    return unless expected["summary_ruleset_version"]

    assert_equal expected["summary_ruleset_version"], summary["ruleset_version"], "#{label}: ruleset_version"
  end

  def check_exposures(expected, events, label)
    exposures = events.select { |e| e["type"] == "exposure" }
    assert_equal expected.size, exposures.size, "#{label}: exposures"
    expected.zip(exposures).each_with_index do |(want, got), i|
      want.each { |field, value| assert_equal value, got[field], "#{label}: exposure #{i} #{field}" }
    end
  end

  def call(call)
    await_ruleset
    case call["op"]
    when "evaluate"
      Array.new(call.fetch("times", 1)) do
        @client.detail(call["flag"], default: call["default"], context: SCENARIOS["context"])
      end
    when "exposure"
      call.fetch("times", 1).times { @client.exposure(call["flag"], context: SCENARIOS["context"]) }
      nil
    when "alias"
      call.fetch("times", 1).times { @client.alias("anonymous", "d-8f1c", "user", "u-1001") }
      nil
    when "flush"
      # Flush waits until every retry its budget allows is answered, so a
      # scenario scripting one response per retry needs it in the
      # background: the mock answers a request only once its own step
      # runs, which must happen while flush is still in flight.
      Thread.new { @client.flush(timeout: 5) }
      nil
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
    (expect["recorded"] || {}).each { |kind, n| assert_equal n, recorded(kind).size, "#{label}: recorded #{kind}" }
  end

  # recorded reads the offline client's recorder for one event kind.
  def recorded(kind)
    case kind
    when "exposure" then @client.recorder.exposures
    when "alias" then @client.recorder.aliases
    else flunk "unknown recorded kind #{kind}"
    end
  end

  # holds? reports whether the SDK's state already meets expect, so that
  # check asserts once the step's response has been applied.
  def holds?(expect)
    status = @client.status
    (expect["stage"].nil? || status.stage.to_s == expect["stage"]) &&
      (expect["stale_since"].nil? || (expect["stale_since"] == "set") == !status.stale_since.nil?) &&
      (expect["answers_of"].nil? || answers?(@client, SCENARIOS["answers"].fetch(expect["answers_of"]))) &&
      (expect["logs"] || {}).all? { |event, n| @log_mutex.synchronize { @logged[event] } >= n } &&
      (expect["recorded"] || {}).all? { |kind, n| recorded(kind).size >= n }
  end

  def answers?(client, answers)
    answers.all? do |a|
      d = observe(client, a)
      d.value == a["value"] && d.variation == a["variation"] && d.reason.to_s == a["reason"] &&
        experiment_of(d) == a["experiment"]
    end
  end

  def assert_answers(client, answers, label)
    answers.each { |a| assert_answer observe(client, a), a, label }
  end

  # observe evaluates an answer's flag for the runner itself, keeping count
  # so that the evaluation summary can be checked against the scenario's
  # own evaluations.
  def observe(client, answer)
    d = client.detail(answer["flag"], default: answer["default"], context: SCENARIOS["context"])
    @observed[[answer["flag"], d.variation, d.reason.to_s]] += 1 if client.equal?(@client)
    d
  end

  # experiment_of is a detail's experiment block as the fixtures write it.
  def experiment_of(detail) = detail.experiment&.as_json

  def assert_answer(detail, want, label)
    msg = "#{label}: #{want["flag"]}"
    assert_equal want["value"], detail.value, "#{msg} value"
    assert_equal want["variation"], detail.variation, "#{msg} variation" if want.key?("variation")
    assert_nil detail.variation, "#{msg} variation" unless want.key?("variation")
    assert_equal want["reason"], detail.reason.to_s, "#{msg} reason"
    if want["experiment"]
      assert_equal want["experiment"], experiment_of(detail), "#{msg} experiment"
    else
      assert_nil detail.experiment, "#{msg} experiment"
    end
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
