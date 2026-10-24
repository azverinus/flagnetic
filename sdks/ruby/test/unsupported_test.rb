# frozen_string_literal: true

require "test_helper"
require "tmpdir"

# A ruleset from a newer server may hold constructs this evaluator does not
# know. The flag serves its default with inconsistent_ruleset, and the
# client says so once per flag and ruleset version, evaluated or not.
class UnsupportedTest < Minitest::Test
  include ClientHelpers

  DIR = File.expand_path("../../../conformance/protocol/degradation", __dir__)

  def newer(version)
    JSON.parse(File.read(File.join(DIR, "ruleset-unknown-operator.json"))).merge("version" => version)
  end

  def unsupported_flags(doc)
    Flagnetic::Eval::Ruleset.decode(JSON.generate(doc)).unsupported_rules.keys.sort
  end

  def test_it_is_logged_once_per_flag_at_load_without_any_evaluation
    doc = newer(7)
    flags = unsupported_flags(doc)
    refute_empty flags
    @server.publish(doc)
    c = client(stream: false)
    assert_equal :fresh, c.ready(timeout: 2)

    lines = logged(:rule_unsupported)
    assert_equal flags, lines.map { |_, f| f[:flag] }.sort
    lines.each do |level, fields|
      assert_equal :error, level
      assert_equal 7, fields[:ruleset_version]
      refute_empty fields[:constructs]
    end

    flags.each { |key| 3.times { c.detail(key, default: "d") } }
    assert_equal flags.size, logged(:rule_unsupported).size, "evaluating logs nothing more"
  end

  def test_the_flag_serves_its_default_with_inconsistent_ruleset
    @server.publish(newer(7))
    c = client(stream: false)
    c.ready(timeout: 2)
    key = unsupported_flags(newer(7)).first
    type = JSON.parse(JSON.generate(newer(7)))["flags"].find { |f| f["key"] == key }["type"]
    default = { "boolean" => false, "string" => "d", "number" => 0 }.fetch(type, {})
    detail = c.detail(key, default: default)
    assert_equal [default, :fallback, :inconsistent_ruleset], [detail.value, detail.reason, detail.error_code]
  end

  def test_a_new_ruleset_version_reports_again_and_the_same_version_does_not
    @server.publish(newer(7))
    c = client(stream: false, timing: { poll_down: 0.05 })
    c.ready(timeout: 2)
    once = logged(:rule_unsupported).size
    @server.on_ruleset { |_| [200, { "Content-Type" => "application/json" }, JSON.generate(newer(7))] }
    eventually { @server.fetches.size >= 4 }
    assert_equal once, logged(:rule_unsupported).size, "a reload of version 7 repeats nothing"

    @server.on_ruleset { |_| [200, { "Content-Type" => "application/json" }, JSON.generate(newer(8))] }
    eventually { logged(:rule_unsupported).size == once * 2 }
    assert_equal [7, 8], logged(:rule_unsupported).map { |_, f| f[:ruleset_version] }.uniq.sort
  end

  def test_a_stored_ruleset_is_reported_at_construction
    Dir.mktmpdir do |dir|
      path = File.join(dir, "ruleset.json")
      File.write(path, JSON.generate(newer(5)))
      @server.on_ruleset { |_| [503, {}, ""] }
      client(store: path, stream: false).ready(timeout: 1)
      assert_equal unsupported_flags(newer(5)), logged(:rule_unsupported).map { |_, f| f[:flag] }.sort
    end
  end
end
