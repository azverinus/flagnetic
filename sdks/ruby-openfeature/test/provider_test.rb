# frozen_string_literal: true

require "test_helper"

# ProviderTest drives the provider through the real OpenFeature client, the
# way an application calls it.
class ProviderTest < Minitest::Test
  include ProviderHelper

  def test_metadata_names_the_provider
    provider, = of_provider
    assert_equal "flagnetic", provider.metadata.name
  end

  def test_ready_through_the_openfeature_sdk
    _, client = of_provider
    assert_equal OF::ProviderState::READY, client.provider_status
  end

  def test_a_rule_is_a_targeting_match
    _, client = of_provider
    d = client.fetch_boolean_details(flag_key: "checkout", default_value: false,
                                     evaluation_context: user(country: "DE"))
    assert_equal [true, "on", "TARGETING_MATCH"], [d.value, d.variant, d.reason]
    assert_equal "rule", d.flag_metadata["flagnetic_reason"]
    assert_equal "fresh", d.flag_metadata["stage"]
    assert_equal 3, d.flag_metadata["ruleset_version"]
  end

  def test_no_match_falls_through_to_default
    _, client = of_provider
    d = client.fetch_boolean_details(flag_key: "checkout", default_value: false,
                                     evaluation_context: user(country: "FR"))
    assert_equal [false, "off", "DEFAULT"], [d.value, d.variant, d.reason]
  end

  def test_an_off_flag_is_disabled
    _, client = of_provider
    d = client.fetch_boolean_details(flag_key: "disabled_flag", default_value: true, evaluation_context: user)
    assert_equal [false, "off", "DISABLED"], [d.value, d.variant, d.reason]
  end

  def test_string
    _, client = of_provider
    d = client.fetch_string_details(flag_key: "greeting", default_value: "def", evaluation_context: user)
    assert_equal %w[hi DEFAULT], [d.value, d.reason]
  end

  def test_number_serves_the_number_as_the_ruleset_has_it
    _, client = of_provider
    assert_equal 0.5, client.fetch_number_value(flag_key: "ratio", default_value: 1, evaluation_context: user)
  end

  def test_integer_narrows_a_whole_number
    _, client = of_provider
    d = client.fetch_integer_details(flag_key: "page_size", default_value: 1, evaluation_context: user)
    assert_kind_of Integer, d.value
    assert_equal [10, "small", "DEFAULT"], [d.value, d.variant, d.reason]
  end

  def test_integer_refuses_a_number_that_is_not_whole
    _, client = of_provider
    d = client.fetch_integer_details(flag_key: "ratio", default_value: 7, evaluation_context: user)
    assert_equal [7, "ERROR", "TYPE_MISMATCH"], [d.value, d.reason, d.error_code]
    assert_nil d.variant
    assert_equal "fallback", d.flag_metadata["flagnetic_reason"]
  end

  def test_integer_of_a_string_flag_is_a_type_mismatch
    _, client = of_provider
    d = client.fetch_integer_details(flag_key: "greeting", default_value: 7, evaluation_context: user)
    assert_equal [7, "ERROR", "TYPE_MISMATCH"], [d.value, d.reason, d.error_code]
  end

  def test_float_widens_a_whole_number
    _, client = of_provider
    d = client.fetch_float_details(flag_key: "page_size", default_value: 1.0, evaluation_context: user)
    assert_kind_of Float, d.value
    assert_equal [10.0, "DEFAULT"], [d.value, d.reason]
  end

  def test_object_resolves_the_json_flag
    _, client = of_provider
    d = client.fetch_object_details(flag_key: "theme", default_value: { "mode" => "system" }, evaluation_context: user)
    assert_equal [{ "mode" => "dark" }, "DEFAULT"], [d.value, d.reason]
  end

  def test_a_flag_not_in_the_ruleset_is_flag_not_found
    _, client = of_provider
    d = client.fetch_boolean_details(flag_key: "no_such_flag", default_value: true, evaluation_context: user)
    assert_equal [true, "ERROR", "FLAG_NOT_FOUND", "flag_not_found"], [d.value, d.reason, d.error_code, d.error_message]
    assert_equal "fallback", d.flag_metadata["flagnetic_reason"]
  end

  def test_a_type_mismatch_is_type_mismatch
    _, client = of_provider
    d = client.fetch_number_details(flag_key: "greeting", default_value: 1, evaluation_context: user)
    assert_equal [1, "ERROR", "TYPE_MISMATCH"], [d.value, d.reason, d.error_code]
  end

  def test_no_ruleset_is_a_general_error
    provider = Flagnetic::OpenFeature::Provider.new("fn_srv_#{"a" * 40}", base_url: "http://127.0.0.1:9",
                                                                          ready_timeout: 0.05, logger: QUIET)
    _, client = register(provider)
    d = client.fetch_boolean_details(flag_key: "checkout", default_value: true, evaluation_context: user)
    assert_equal [true, "ERROR", "GENERAL", "no_ruleset"], [d.value, d.reason, d.error_code, d.error_message]
    assert_equal({ "flagnetic_reason" => "fallback", "stage" => "built_in", "overridden" => false }, d.flag_metadata)
  end

  def test_an_experiment_is_a_split_carrying_its_block
    _, client = of_provider
    d = client.fetch_string_details(flag_key: "pricing", default_value: "x", evaluation_context: user)
    assert_equal %w[new treatment SPLIT], [d.value, d.variant, d.reason]
    assert_equal "pricing_test", d.flag_metadata["experiment_key"]
    assert_equal "u-1", d.flag_metadata["experiment_unit_id"]
    refute d.flag_metadata.key?("experiment_inclusion_salt")
  end

  def test_the_units_attribute_adds_further_unit_types
    _, client = of_provider
    d = client.fetch_string_details(flag_key: "pricing", default_value: "x",
                                    evaluation_context: OF::EvaluationContext.new(units: { "user" => "u-9" }))
    assert_equal "u-9", d.flag_metadata["experiment_unit_id"]
  end

  def test_a_custom_unit_type_changes_which_unit_targeting_key_names
    _, client = of_provider(unit_type: "anonymous")
    d = client.fetch_string_details(flag_key: "pricing", default_value: "x", evaluation_context: user)
    assert_equal %w[old DEFAULT], [d.value, d.reason]
  end

  def test_a_local_override_is_static
    provider, client = of_provider(allow_overrides: true)
    provider.client.local_overrides.set("greeting", "yo")
    d = client.fetch_string_details(flag_key: "greeting", default_value: "def", evaluation_context: user)
    assert_equal %w[yo STATIC], [d.value, d.reason]
    assert_equal "local_override", d.flag_metadata["flagnetic_reason"]
  end

  def test_a_client_key_is_refused_as_the_client_refuses_it
    assert_raises(ArgumentError) do
      Flagnetic::OpenFeature::Provider.new("fn_cli_#{"a" * 40}", base_url: "http://127.0.0.1:9")
    end
  end
end
