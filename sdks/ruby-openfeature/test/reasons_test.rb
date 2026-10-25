# frozen_string_literal: true

require "test_helper"

# ReasonsTest runs first what every Flagnetic provider in every language
# must agree on: the shared reason mapping, then every conformance case
# served through the provider with its reason mapped by it.
class ReasonsTest < Minitest::Test
  MAPPING = JSON.parse(File.read(File.join(CONFORMANCE, "openfeature/reasons.json"), encoding: "UTF-8"))
  FIXTURES = File.join(CONFORMANCE, "fixtures")
  FILES = (Dir.glob("**/*.json", base: FIXTURES) - %w[murmur3.json regex.json]).sort
  DEFAULTS = { "boolean" => false, "string" => "conformance-default", "number" => -1.0,
               "json" => { "conformance" => "default" } }.freeze

  def test_every_reason_maps_as_the_shared_fixture_says
    assert_equal MAPPING.fetch("metadata_key"), Flagnetic::OpenFeature::REASON_METADATA_KEY
    MAPPING.fetch("reasons").each do |flagnetic_reason, openfeature_reason|
      assert_equal openfeature_reason, Flagnetic::OpenFeature.reason_of(flagnetic_reason), flagnetic_reason
      assert_equal openfeature_reason, Flagnetic::OpenFeature.reason_of(flagnetic_reason.to_sym), flagnetic_reason
    end
  end

  def test_a_reason_a_newer_server_adds_is_unknown
    assert_equal OF::Provider::Reason::UNKNOWN, Flagnetic::OpenFeature.reason_of(:a_reason_a_newer_server_adds)
  end

  def test_only_the_provider_and_the_reason_mapping_are_public
    assert_equal %i[Provider REASON_METADATA_KEY VERSION], Flagnetic::OpenFeature.constants.sort
    assert_equal %i[reason_of], Flagnetic::OpenFeature.singleton_methods
  end

  def test_fixtures_are_found
    refute_empty FILES
  end

  FILES.each do |rel|
    define_method("test_#{rel.delete_suffix(".json").tr("/-", "__")}_through_the_provider") do
      run_fixture(rel, JSON.parse(File.read(File.join(FIXTURES, rel), encoding: "UTF-8")))
    end
  end

  private

  def run_fixture(rel, data)
    provider = Flagnetic::OpenFeature::Provider.new(nil, offline: data.fetch("ruleset"), logger: QUIET)
    types = data.fetch("ruleset").fetch("flags", []).to_h { |f| [f.fetch("key"), f.fetch("type")] }
    data.fetch("cases").each do |kase|
      type = types.fetch(kase.fetch("flag"), "boolean")
      run_case(provider, type, kase, "#{rel} #{kase.fetch("name")}")
    end
  ensure
    provider&.shutdown
  end

  def run_case(provider, type, kase, name)
    expect = kase.fetch("expect")
    got = resolve(provider, type, kase.fetch("flag"), context(kase.fetch("context")))
    assert_equal MAPPING.fetch("reasons").fetch(expect.fetch("reason")), got.reason, name
    if expect.fetch("reason") == "error"
      want = expect.fetch("error_code") == "flag_not_found" ? "FLAG_NOT_FOUND" : "GENERAL"
      assert_equal [want, DEFAULTS.fetch(type)], [got.error_code, got.value], name
      assert_equal "fallback", got.flag_metadata.fetch("flagnetic_reason"), name
    else
      assert_equal expect.fetch("value"), got.value, name
      assert_equal expect["variation"], got.variant, name
      assert_equal expect.fetch("reason"), got.flag_metadata.fetch("flagnetic_reason"), name
      assert_nil got.error_code, name
    end
  end

  # context routes every unit through the units attribute, so no case
  # needs a unit type of its own.
  def context(raw)
    OF::EvaluationContext.new(**(raw["attributes"] || {}).transform_keys(&:to_sym), units: raw["units"] || {})
  end

  def resolve(provider, type, flag, context)
    method = { "boolean" => :fetch_boolean_value, "string" => :fetch_string_value,
               "number" => :fetch_float_value, "json" => :fetch_object_value }.fetch(type)
    provider.public_send(method, flag_key: flag, default_value: DEFAULTS.fetch(type), evaluation_context: context)
  end
end
