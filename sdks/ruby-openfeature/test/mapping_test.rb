# frozen_string_literal: true

require "test_helper"

# MappingTest covers the translation of a served Flagnetic::Detail into an
# OpenFeature resolution on details built directly: error codes, the
# variant, and the flag metadata.
class MappingTest < Minitest::Test
  def resolution(**fields)
    Flagnetic::OpenFeature.resolution(Flagnetic::Detail.new(value: true, reason: :fallthrough, ruleset_version: 3,
                                                            stage: :fresh, **fields))
  end

  def test_fallback_is_error_with_the_variant_left_unset
    r = resolution(reason: :fallback, error_code: :general, value: "def", variation: nil)
    assert_equal "ERROR", r.reason
    assert_nil r.variant
    assert_equal "def", r.value
    assert_equal "general", r.error_message
  end

  def test_flag_not_found_and_type_mismatch_keep_their_openfeature_identity
    assert_equal "FLAG_NOT_FOUND", resolution(reason: :fallback, error_code: :flag_not_found).error_code
    assert_equal "TYPE_MISMATCH", resolution(reason: :fallback, error_code: :type_mismatch).error_code
  end

  def test_every_other_error_code_is_general
    %i[server_only prerequisite_cycle inconsistent_ruleset no_ruleset general].each do |code|
      assert_equal "GENERAL", resolution(reason: :fallback, error_code: code).error_code, code
    end
  end

  def test_a_served_value_has_no_error_code
    r = resolution(reason: :rule, variation: "on")
    assert_nil r.error_code
    assert_nil r.error_message
    assert_equal "on", r.variant
  end

  def test_flag_metadata_carries_reason_stage_ruleset_version_variation_and_overridden
    r = resolution(reason: :rule, variation: "on", overridden: true)
    assert_equal({ "flagnetic_reason" => "rule", "stage" => "fresh", "ruleset_version" => 3, "variation" => "on",
                   "overridden" => true }, r.flag_metadata)
  end

  def test_flag_metadata_leaves_out_what_the_detail_has_none_of
    r = resolution(reason: :fallback, error_code: :no_ruleset, ruleset_version: nil, stage: :built_in)
    assert_equal({ "flagnetic_reason" => "fallback", "stage" => "built_in", "overridden" => false }, r.flag_metadata)
  end

  def test_flag_metadata_flattens_the_experiment_block
    experiment = Flagnetic::Eval::ExperimentDetail.new(key: "pricing_test", variation: "treatment", unit_type: "user",
                                                       unit_id: "u-1", inclusion_salt: "in", variation_salt: "var",
                                                       inclusion_bucket: 10, variation_bucket: 20,
                                                       override: false)
    r = resolution(reason: :experiment, variation: "treatment", experiment: experiment)
    assert_equal({ "experiment_key" => "pricing_test", "experiment_variation" => "treatment",
                   "experiment_unit_type" => "user", "experiment_unit_id" => "u-1",
                   "experiment_inclusion_bucket" => 10, "experiment_variation_bucket" => 20,
                   "experiment_override" => false }, r.flag_metadata.select { |k, _| k.start_with?("experiment_") })
  end
end
