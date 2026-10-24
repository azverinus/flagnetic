# frozen_string_literal: true

require "test_helper"
require "tmpdir"

# The public surface: getters, detail, local overrides, the store, the
# logger hook and the testing module.
class ClientTest < Minitest::Test
  include ClientHelpers

  def offline(values = {}, protected: false, **)
    Flagnetic::Client.new(offline: document(1, values, protected: protected), logger: method(:record), **)
  end

  # --- getters ------------------------------------------------------------

  def test_typed_getters_take_keyword_arguments
    c = offline({ "on" => true, "copy" => "hi", "limit" => 5, "config" => { "a" => [1] } })
    assert c.bool("on", default: false)
    assert_equal "hi", c.string("copy", default: "")
    assert_equal 5, c.number("limit", default: 0)
    assert_equal({ "a" => [1] }, c.json("config", default: {}))
    assert_predicate c.json("config", default: {}), :frozen?
    assert c.bool(:on, default: false, context: { units: { user: "u-1" } })
  end

  def test_int_serves_a_whole_number_as_an_integer
    c = offline({ "limit" => 5, "float_limit" => 5.0, "ratio" => 2.5, "copy" => "hi" })
    assert_equal 5, c.int("limit", default: 0)
    limit = c.int("float_limit", default: 0)
    assert_equal 5, limit
    assert_kind_of Integer, limit
    assert_equal 7, c.int("ratio", default: 7), "a number that is not whole serves the default"
    assert_equal 7, c.int("copy", default: 7)
    assert_equal 7, c.int("absent", default: 7)
    assert_equal 2, logged(:type_mismatch).size
  end

  def test_the_detail_of_a_served_value
    doc = JSON.parse(File.read(File.expand_path("../../../conformance/fixtures/overrides/influence.json", __dir__)))
    fixture_case = doc["cases"].find { |k| k["expect"]["override_influenced"] && k["expect"]["experiment"] }
    c = Flagnetic::Client.new(offline: doc["ruleset"])
    default = { String => "", TrueClass => false, FalseClass => true }.fetch(fixture_case["expect"]["value"].class)
    d = c.detail(fixture_case["flag"], default: default, context: fixture_case["context"])

    assert_equal fixture_case["expect"]["value"], d.value
    assert_equal fixture_case["expect"]["reason"].to_sym, d.reason
    assert_equal fixture_case["expect"]["variation"], d.variation
    assert d.overridden
    assert_equal fixture_case["expect"]["experiment"], d.experiment.as_json
    assert_equal [doc["ruleset"]["version"], :fresh], [d.ruleset_version, d.stage]
    assert_kind_of Time, d.received_at
    assert_predicate d, :frozen?
  end

  def test_a_type_mismatch_serves_the_default_and_logs_once
    c = offline({ "copy" => "hi" })
    2.times { assert c.bool("copy", default: true) }
    d = c.detail("copy", default: 1)
    assert_equal [1, :fallback, :type_mismatch], [d.value, d.reason, d.error_code]
    assert_equal [[:warn, { flag: "copy", flag_type: "string", requested_type: "boolean" }]], logged(:type_mismatch)
  end

  def test_an_evaluator_error_serves_the_default_and_logs_rule_unsupported_once
    doc = document(1, { "on" => true })
    doc["flags"][0]["rules"] = [{ "id" => "r1", "serve" => { "kind" => "variation", "variation" => "value" },
                                  "conditions" => [{ "kind" => "attribute", "attribute" => "x", "op" => "sounds_like",
                                                     "value" => "y" }] }]
    c = Flagnetic::Client.new(offline: doc, logger: method(:record))
    2.times do
      d = c.detail("on", default: false)
      assert_equal [false, :fallback, :inconsistent_ruleset], [d.value, d.reason, d.error_code]
    end
    assert_equal 1, logged(:rule_unsupported).size
  end

  def test_getters_never_raise
    raising = ->(*) { raise "logger down" }
    c = Flagnetic::Client.new(offline: document(1, { "on" => true }), logger: raising)
    d = c.detail("on", default: false, context: 42)
    assert_equal [false, :fallback, :general], [d.value, d.reason, d.error_code]
    refute c.bool("on", default: false, context: Object.new)
    c.on_change { raise "listener down" }
    assert_nil c.local_overrides.clear
  end

  def test_an_internal_failure_is_logged_as_evaluation_failed
    c = offline({ "on" => true })
    refute c.bool("on", default: false, context: 42)
    assert_equal "on", logged(:evaluation_failed).first[1][:flag]
  end

  def test_a_raising_listener_is_logged
    c = offline({ "on" => true }, allow_overrides: true)
    c.on_change { raise "listener down" }
    c.local_overrides.set("on", false)
    assert_match(/listener down/, logged(:listener_failed).first[1][:cause])
  end

  def test_listeners_can_be_removed
    c = offline({ "on" => true }, allow_overrides: true)
    seen = []
    remove = c.on_change { |keys| seen << keys }
    c.local_overrides.set("on", false)
    remove.call
    c.local_overrides.set("on", true)
    assert_equal [["on"]], seen
  end

  # --- local overrides ----------------------------------------------------

  def test_local_overrides_need_the_explicit_option
    c = offline({ "on" => true })
    assert_raises(Flagnetic::LocalOverridesNotAllowed) { c.local_overrides.set("on", false) }
    assert_equal [[:warn, { flag: "on", reason: "not_allowed" }]], logged(:override_refused)
    assert c.bool("on", default: false)
  end

  def test_a_local_override_serves_its_value_with_reason_local_override
    c = offline({ "on" => true, "limit" => 5 }, allow_overrides: true)
    assert_equal [[:warn, {}]], logged(:local_overrides_allowed)
    changes = []
    c.on_change { |keys| changes << keys }

    c.local_overrides.set("on", false)
    c.local_overrides.set("absent", "value")
    d = c.detail("on", default: true)
    assert_equal [false, :local_override, nil, nil, :fresh], [d.value, d.reason, d.variation, d.experiment, d.stage]
    assert_equal "value", c.string("absent", default: "")
    assert_equal({ "on" => false, "absent" => "value" }, c.local_overrides.list)
    assert_equal [:warn, { flag: "on" }], logged(:override_set).first

    assert_raises(ArgumentError) { c.local_overrides.set("limit", "five") }
    d = c.detail("absent", default: 1)
    assert_equal %i[fallback type_mismatch], [d.reason, d.error_code]

    c.local_overrides.remove("on")
    assert c.bool("on", default: false)
    c.local_overrides.clear
    assert_empty c.local_overrides.list
    assert_equal [["on"], ["absent"], ["on"], ["absent"]], changes
  end

  def test_a_protected_environment_refuses_local_overrides
    c = offline({ "on" => true }, protected: true, allow_overrides: true)
    error = assert_raises(Flagnetic::LocalOverrideRefused) { c.local_overrides.set("on", false) }
    assert_match(/protected/, error.message)
    assert_equal [:warn, { flag: "on", reason: "protected", environment: "development" }],
                 logged(:override_refused).first
    assert c.bool("on", default: false)
  end

  def test_a_protected_ruleset_arriving_drops_the_local_overrides
    doc = document(1, { "on" => true })
    c = Flagnetic::Client.new(offline: -> { JSON.generate(doc) }, allow_overrides: true, logger: method(:record))
    c.local_overrides.set("on", false)
    changes = []
    c.on_change { |keys| changes << keys }

    doc = document(2, { "on" => true }, protected: true)
    c.reload
    assert c.bool("on", default: false)
    assert_empty c.local_overrides.list
    assert_equal [["on"]], changes
    assert_equal 1, logged(:override_refused).size
  end

  def test_local_overrides_on_a_client_fed_by_the_server
    @server.publish(document(1, { "on" => true }))
    c = client(allow_overrides: true)
    c.ready(timeout: 2)
    c.local_overrides.set("on", false)
    refute c.bool("on", default: true)
    assert_equal :fresh, c.detail("on", default: true).stage
  end

  # --- the store ----------------------------------------------------------

  def test_the_store_serves_last_good_before_any_network_call_and_persists_every_accepted_ruleset
    Dir.mktmpdir do |dir|
      path = File.join(dir, "ruleset.json")
      File.write(path, JSON.generate(document(1, { "on" => true })))
      File.utime(Time.at(1_700_000_000), Time.at(1_700_000_000), path)
      @server.on_ruleset { |_| [503, {}, ""] }

      c = client(store: path)
      d = c.detail("on", default: false)
      assert_equal [true, :last_good, 1, Time.at(1_700_000_000)], [d.value, d.stage, d.ruleset_version, d.received_at]
      assert_equal :last_good, c.ready(timeout: 2)
      assert_equal '"1"', @server.fetches.first["if-none-match"]

      served = JSON.generate(document(2, { "on" => false }))
      @server.on_ruleset { |_| [200, {}, served] }
      @server.emit(2)
      eventually { c.stage == :fresh }
      assert_equal served, File.read(path)
      assert_empty Dir.glob(File.join(dir, "*.tmp"))
    end
  end

  def test_a_corrupt_store_starts_built_in_with_a_log
    Dir.mktmpdir do |dir|
      path = File.join(dir, "ruleset.json")
      File.write(path, "{ not json")
      @server.on_ruleset { |_| [503, {}, ""] }
      c = client(store: path)
      assert_equal :built_in, c.stage
      assert_equal path, logged(:store_failed).first[1][:path]
    end
  end

  def test_a_missing_store_file_is_no_error
    Dir.mktmpdir do |dir|
      @server.publish(document(1, { "on" => true }))
      path = File.join(dir, "ruleset.json")
      c = client(store: path)
      assert_equal :fresh, c.ready(timeout: 2)
      assert_empty logged(:store_failed)
      assert File.exist?(path)
    end
  end

  # --- logging ------------------------------------------------------------

  def test_the_standard_logger_writes_warn_and_above_to_stderr
    _, err = capture_io do
      c = Flagnetic::Client.new(offline: document(1, { "copy" => "hi" }))
      c.bool("copy", default: false)
    end
    assert_match(/type_mismatch flag="copy"/, err)
    refute_match(/ruleset_loaded/, err)
  end

  def test_a_logger_object_receives_every_level
    lines = []
    logger = Object.new
    logger.define_singleton_method(:add) { |severity, message| lines << [severity, message] }
    Flagnetic::Client.new(offline: document(1), logger: logger)
    assert_equal [1, "ruleset_loaded source=\"offline\" ruleset_version=1 flags=0"], lines.first
  end

  def test_every_logged_event_has_a_stable_name
    @server.publish(document(1, { "on" => true }))
    c = client(allow_overrides: true)
    c.ready(timeout: 2)
    c.string("on", default: "")
    @server.close_streams
    eventually { logged(:sse_connected).size == 2 }
    names = @log_mutex.synchronize { @logged.map { |e| e[1] } }.uniq
    assert_empty names - Flagnetic::EVENTS
    assert(names.all? { |n| Flagnetic::LEVELS.include?(@logged.find { |e| e[1] == n }[0]) })
  end

  # --- offline ------------------------------------------------------------

  def test_an_offline_client_makes_no_request
    Dir.mktmpdir do |dir|
      path = File.join(dir, "flags.json")
      File.write(path, JSON.generate(document(1, { "on" => true })))
      c = Flagnetic::Client.new(offline_file: path)
      assert_equal :fresh, c.ready(timeout: 0)
      assert c.bool("on", default: false)
      File.write(path, JSON.generate(document(2, { "on" => false })))
      c.reload
      refute c.bool("on", default: true)
      c.close
    end
    assert_empty @server.requests
  end

  def test_a_broken_offline_document_raises_on_reload_and_keeps_the_ruleset
    doc = JSON.generate(document(1, { "on" => true }))
    c = Flagnetic::Client.new(offline: -> { doc })
    doc = "{"
    assert_raises(Flagnetic::OfflineError) { c.reload }
    assert c.bool("on", default: false)
  end

  def test_an_offline_source_that_raises_never_fails_construction
    calls = 0
    source = lambda do
      calls += 1
      raise "source down" if calls == 1

      JSON.generate(document(1, { "on" => true }))
    end
    c = Flagnetic::Client.new(offline: source, logger: method(:record))
    assert_equal :built_in, c.stage
    assert_match(/source down/, logged(:fetch_failed).first[1][:cause])

    c.reload
    assert c.bool("on", default: false)
    calls = 0
    error = assert_raises(Flagnetic::OfflineError) { c.reload }
    assert_equal "offline ruleset: RuntimeError: source down", error.message
    assert c.bool("on", default: false)
  end

  def test_a_ruleset_nested_deeper_than_the_json_default_is_accepted
    deep = 150.times.reduce(1) { |inner, _| [inner] }
    c = offline({ "config" => deep })
    assert_equal :fresh, c.stage
    assert_equal deep, c.json("config", default: nil)
  end

  def test_inspect_never_shows_the_sdk_key
    c = client(stream: false)
    refute_includes c.inspect, "a" * 40
  end

  def test_close_from_a_listener_stops_the_client
    @server.publish(document(1, { "on" => true }))
    c = client
    closed = Queue.new
    c.on_stage do |stage|
      next unless stage == :fresh

      c.close(timeout: 1)
      closed << true
    end
    assert pop(closed, 3)
    assert_empty logged(:listener_failed)
    requests = @server.requests.size
    sleep 0.3
    assert_equal requests, @server.requests.size
  end
end
