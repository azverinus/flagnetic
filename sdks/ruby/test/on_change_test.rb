# frozen_string_literal: true

require "test_helper"

# on_change names every flag whose evaluation may have changed: through its
# own configuration, a segment, a declared attribute or unit type, or a
# prerequisite, transitively.
class OnChangeTest < Minitest::Test
  def base
    {
      "schema_version" => 1, "version" => 1, "scope" => "server", "unit_types" => %w[user device],
      "attributes" => [{ "key" => "plan", "type" => "string" }, { "key" => "age", "type" => "number" }],
      "segments" => [
        { "key" => "pro", "conditions" => [{ "kind" => "attribute", "attribute" => "plan", "op" => "eq",
                                             "value" => "pro" }] },
        { "key" => "wrapper", "conditions" => [{ "kind" => "in_segment", "segment" => "pro" }] }
      ],
      "flags" => [
        rule_flag("by_segment", { "kind" => "in_segment", "segment" => "wrapper" }),
        rule_flag("by_age", { "kind" => "attribute", "attribute" => "age", "op" => "gt", "value" => 18 }),
        rule_flag("by_device_rollout", nil, serve: { "kind" => "rollout", "variation" => "true",
                                                     "unit_type" => "device", "percentage" => 50, "salt" => "s" }),
        rule_flag("needs_segment_flag", { "kind" => "prerequisite", "flag" => "by_segment", "variation" => "true" }),
        rule_flag("needs_needs", nil).merge("prerequisites" => [{ "flag" => "needs_segment_flag",
                                                                  "variation" => "true" }]),
        rule_flag("unrelated", nil)
      ]
    }
  end

  def rule_flag(key, condition, serve: { "kind" => "variation", "variation" => "true" })
    {
      "key" => key, "type" => "boolean", "on" => true,
      "variations" => [{ "key" => "true", "value" => true }, { "key" => "false", "value" => false }],
      "off_variation" => "false", "fallthrough" => "false",
      "rules" => [{ "id" => "r1", "conditions" => [condition].compact, "serve" => serve }]
    }
  end

  # changes loads base, applies edit to a copy and returns what on_change
  # received on reload.
  def changes(&)
    doc = base
    client = Flagnetic::Client.new(offline: -> { JSON.generate(doc) })
    received = []
    client.on_change { |keys| received << keys }
    doc = base.tap(&)
    client.reload
    received
  end

  def test_a_segment_condition_edit_reaches_the_flag_and_its_dependents
    received = changes { |d| d["segments"][0]["conditions"][0]["value"] = "team" }
    assert_equal [%w[by_segment needs_needs needs_segment_flag]], received
  end

  def test_an_attribute_declared_type_edit
    received = changes { |d| d["attributes"][1]["type"] = "string" }
    assert_equal [["by_age"]], received
  end

  def test_a_unit_type_declaration_edit
    received = changes { |d| d["unit_types"] = ["user"] }
    assert_equal [["by_device_rollout"]], received
  end

  def test_a_flag_edit_reaches_its_dependents_only
    received = changes { |d| d["flags"][3]["on"] = false }
    assert_equal [%w[needs_needs needs_segment_flag]], received
  end

  def test_added_and_removed_flags
    received = changes do |d|
      d["flags"].delete_at(5)
      d["flags"] << rule_flag("brand_new", nil)
    end
    assert_equal [%w[brand_new unrelated]], received
  end

  def test_an_unchanged_ruleset_fires_nothing
    received = changes { |d| d["version"] = 2 }
    assert_empty received
  end
end
