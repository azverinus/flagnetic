# frozen_string_literal: true

require "test_helper"

# ContextTest covers how an OpenFeature evaluation context becomes a
# Flagnetic context: the targeting key is the unit ID of the provider's
# unit type, the units attribute adds further unit IDs by unit type, and
# every other attribute is an attribute.
class ContextTest < Minitest::Test
  MAPPING = Flagnetic::OpenFeature.const_get(:Mapping)

  def convert(context, unit_type = "user") = MAPPING.context(context, unit_type)

  def test_targeting_key_becomes_the_unit_id_of_unit_type
    assert_equal({ units: { "user" => "u-1" }, attributes: {} },
                 convert(OF::EvaluationContext.new(targeting_key: "u-1")))
  end

  def test_units_attribute_adds_further_unit_type_id_pairs
    got = convert(OF::EvaluationContext.new(targeting_key: "u-1", units: { "anonymous" => "d-1", device: "x-1" }))
    assert_equal({ "user" => "u-1", "anonymous" => "d-1", "device" => "x-1" }, got[:units])
  end

  def test_the_targeting_key_wins_over_the_same_unit_type_in_units
    got = convert(OF::EvaluationContext.new(targeting_key: "u-1", units: { "user" => "u-2" }))
    assert_equal({ "user" => "u-1" }, got[:units])
  end

  def test_every_other_field_becomes_an_attribute
    got = convert(OF::EvaluationContext.new(targeting_key: "u-1", country: "DE", plan: "pro"))
    assert_equal({ "country" => "DE", "plan" => "pro" }, got[:attributes])
  end

  def test_a_unit_id_that_is_not_a_string_is_dropped
    assert_empty convert(OF::EvaluationContext.new(units: { "anonymous" => 123 }))[:units]
  end

  def test_units_that_are_not_a_hash_are_ignored
    assert_equal({ units: {}, attributes: {} }, convert(OF::EvaluationContext.new(units: "d-1")))
  end

  def test_an_empty_targeting_key_names_no_unit
    assert_empty convert(OF::EvaluationContext.new(targeting_key: "", country: "DE"))[:units]
  end

  def test_no_context_is_an_empty_one
    assert_equal({ units: {}, attributes: {} }, convert(nil))
  end

  def test_a_custom_unit_type_changes_which_unit_targeting_key_names
    assert_equal({ "device" => "d-1" }, convert(OF::EvaluationContext.new(targeting_key: "d-1"), "device")[:units])
  end
end
