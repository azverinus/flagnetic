# frozen_string_literal: true

require "test_helper"
require "tmpdir"
require "flagnetic/testing"

# Flagnetic::Testing.offline: clients for tests, from values, a ruleset
# document or a file, whose flags a test rewrites with set and restores
# with reload, and whose recorder holds what they would have sent.
class TestingTest < Minitest::Test
  include ClientHelpers

  FIXTURE = JSON.parse(File.read(File.expand_path("../../../conformance/fixtures/experiments/automatic-exposure.json",
                                                  __dir__)))
  ASSIGNED = { units: { "anonymous" => "u_10482" } }.freeze

  def test_offline_from_values_with_set_reload_and_on_change
    c = Flagnetic::Testing.offline(values: { "on" => true, "copy" => "a" })
    changes = []
    c.on_change { |keys| changes << keys }

    c.set("copy", "b")
    d = c.detail("copy", default: "")
    assert_equal ["b", :fallthrough], [d.value, d.reason]
    assert_raises(ArgumentError) { c.set("copy", 1) }
    c.set("added", 3)
    assert_equal 3, c.number("added", default: 0)

    c.reload
    assert_equal "a", c.string("copy", default: "")
    assert_equal 0, c.number("added", default: 0)
    assert_equal [["copy"], ["added"], %w[added copy]], changes
  end

  def test_the_recorder_counts_every_evaluation_until_reset
    c = Flagnetic::Testing.offline(values: { "on" => true })
    c.bool("on", default: false)
    c.bool("missing", default: false)
    rows = c.recorder.summary.map { |r| [r.flag, r.variation, r.reason, r.count] }
    assert_equal [["missing", nil, :fallback, 1], ["on", "value", :fallthrough, 1]], rows
    c.recorder.reset
    assert_empty c.recorder.summary
  end

  def test_the_recorder_counts_a_local_override_under_local_override
    c = Flagnetic::Testing.offline(values: { "on" => true }, allow_overrides: true, logger: ->(*) {})
    c.local_overrides.set("on", false)
    c.bool("on", default: true)
    row = c.recorder.summary.find { |r| r.reason == :local_override }
    assert_equal ["on", nil, 1], [row.flag, row.variation, row.count]
  end

  def test_the_recorder_holds_exposures_and_aliases_and_summaries_on_flush
    c = Flagnetic::Testing.offline(FIXTURE["ruleset"])
    c.string("auto_off", default: "", context: ASSIGNED)
    c.exposure("auto_off", context: ASSIGNED)
    c.alias("anonymous", "u_10482", "user", "u-1001")
    assert_equal([%w[checkout_v2 control u_10482]],
                 c.recorder.exposures.map { |e| [e.experiment_key, e.variation_key, e.unit_id] })
    assert_equal([%w[u_10482 u-1001]], c.recorder.aliases.map { |a| [a.from_unit_id, a.to_unit_id] })
    assert_empty c.recorder.evaluation_summaries
    c.flush
    assert_equal 1, c.recorder.evaluation_summaries.size
  end

  def test_offline_from_a_document_and_a_file
    doc = document(1, { "on" => true })
    assert Flagnetic::Testing.offline(doc).bool("on", default: false)
    assert Flagnetic::Testing.offline(JSON.generate(doc)).bool("on", default: false)
    Dir.mktmpdir do |dir|
      path = File.join(dir, "f.json")
      File.write(path, JSON.generate(doc))
      c = Flagnetic::Testing.offline(file: path)
      assert c.bool("on", default: false)

      File.write(path, JSON.generate(document(2, { "on" => false })))
      c.reload
      refute c.bool("on", default: true)
    end
  end

  def test_offline_refuses_a_bad_document_and_a_missing_source
    assert_raises(Flagnetic::OfflineError) { Flagnetic::Testing.offline("{") }
    assert_raises(ArgumentError) { Flagnetic::Testing.offline }
    assert_raises(ArgumentError) { Flagnetic::Testing.offline(document(1), values: {}) }
  end
end
