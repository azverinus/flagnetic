# frozen_string_literal: true

require "test_helper"

# A condition this evaluator cannot compile, a pattern it refuses, makes its
# flag serve the default here only. The client says so once per flag and
# ruleset version when the ruleset loads, evaluated or not.
class InvalidConditionTest < Minitest::Test
  include ClientHelpers

  FIXTURE = File.expand_path("../../../conformance/fixtures/errors/codes.json", __dir__)

  def ruleset(version)
    JSON.parse(File.read(FIXTURE, encoding: "UTF-8")).fetch("ruleset").merge("version" => version)
  end

  def broken_flags(doc)
    Flagnetic::Eval::Ruleset.decode(JSON.generate(doc)).invalid_conditions.keys.sort
  end

  def test_the_evaluator_reports_the_reason_by_flag
    report = Flagnetic::Eval::Ruleset.decode(JSON.generate(ruleset(1))).invalid_conditions
    assert_equal 1, report.fetch("invalid_regex").size
    assert_includes report.fetch("invalid_regex").first, "operator \"matches\""
    assert_includes report.fetch("undeclared_attribute").first, "is not declared"
    refute_includes report.keys, "fine"
  end

  def test_a_supported_ruleset_reports_nothing
    path = File.join(__dir__, "../../../conformance/fixtures/operators/string.json")
    doc = JSON.parse(File.read(path, encoding: "UTF-8")).fetch("ruleset")
    assert_empty Flagnetic::Eval::Ruleset.decode(JSON.generate(doc)).invalid_conditions
  end

  def test_it_is_logged_once_per_flag_at_load_without_any_evaluation
    flags = broken_flags(ruleset(7))
    refute_empty flags
    @server.publish(ruleset(7))
    c = client(stream: false)
    assert_equal :fresh, c.ready(timeout: 2)

    lines = logged(:condition_invalid)
    assert_equal flags, lines.map { |_, f| f[:flag] }.sort
    lines.each do |level, fields|
      assert_equal :error, level
      assert_equal 7, fields[:ruleset_version]
      refute_empty fields[:conditions]
    end
    assert_empty logged(:rule_unsupported)

    flags.each { |key| 3.times { c.detail(key, default: "d") } }
    assert_equal flags.size, logged(:condition_invalid).size, "evaluating logs nothing more"
  end

  def test_a_new_ruleset_version_reports_again_and_the_same_version_does_not
    @server.publish(ruleset(7))
    c = client(stream: false, timing: { poll_down: 0.05 })
    c.ready(timeout: 2)
    once = logged(:condition_invalid).size
    @server.on_ruleset { |_| [200, { "Content-Type" => "application/json" }, JSON.generate(ruleset(7))] }
    eventually { @server.fetches.size >= 4 }
    assert_equal once, logged(:condition_invalid).size, "a reload of version 7 repeats nothing"

    @server.on_ruleset { |_| [200, { "Content-Type" => "application/json" }, JSON.generate(ruleset(8))] }
    eventually { logged(:condition_invalid).size == once * 2 }
    assert_equal [7, 8], logged(:condition_invalid).map { |_, f| f[:ruleset_version] }.uniq.sort
  end
end
