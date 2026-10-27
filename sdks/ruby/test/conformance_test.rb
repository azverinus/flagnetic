# frozen_string_literal: true

require "test_helper"

# ConformanceTest runs every fixture of the language-agnostic conformance
# suite in conformance/. There is no skip list: a fixture added with an
# evaluator change fails here until this evaluator implements it.
# murmur3.json holds hash vectors and regex.json RE2 vectors; every other
# file is a ruleset with cases, each compared exactly against the
# evaluation result, and the report of unsupported rules it expects.
class ConformanceTest < Minitest::Test
  FIXTURES = File.expand_path("../../../conformance/fixtures", __dir__)
  REASONS = File.expand_path("../../../conformance/openfeature/reasons.json", __dir__)

  FILES = Dir.glob("**/*.json", base: FIXTURES).sort

  def test_fixtures_are_found
    refute_empty FILES, "no fixtures under #{FIXTURES}"
    assert_includes FILES, "murmur3.json"
  end

  FILES.each do |rel|
    name = rel.delete_suffix(".json")
    define_method("test_#{name.tr("/-", "__")}") do
      data = JSON.parse(File.read(File.join(FIXTURES, rel), encoding: "UTF-8"))
      case rel
      when "murmur3.json" then run_murmur(data)
      when "regex.json" then run_regex(data)
      else run_fixture(name, data)
      end
    end
  end

  # Every reason an evaluator can produce has an OpenFeature reason in the
  # shared mapping, so a provider built on this evaluator can map any
  # result.
  def test_openfeature_reasons_cover_every_evaluator_reason
    data = JSON.parse(File.read(REASONS, encoding: "UTF-8"))
    assert_equal "flagnetic_reason", data.fetch("metadata_key")
    mapping = data.fetch("reasons")
    Flagnetic::Eval::REASONS.each do |reason|
      assert_includes mapping.keys, reason.to_s
    end
    assert_empty mapping.values - %w[TARGETING_MATCH SPLIT DEFAULT DISABLED ERROR STATIC]
  end

  private

  def run_murmur(data)
    assert_keys data, %w[name source vectors]
    vectors = data.fetch("vectors")
    refute_empty vectors
    vectors.each do |vec|
      assert_keys vec, %w[input hash bucket]
      input = vec.fetch("input")
      assert_equal vec.fetch("hash"), Flagnetic::Eval.const_get(:Murmur3).hash32(input), "hash32(#{input.inspect})"
      assert_equal vec.fetch("bucket"), vec.fetch("hash") % Flagnetic::Eval::BUCKETS, "vector #{input.inspect}"
      next unless input.include?(".")

      salt, unit_id = input.split(".", 2)
      assert_equal vec.fetch("bucket"), Flagnetic::Eval.const_get(:Murmur3).bucket(salt, unit_id),
                   "bucket(#{salt.inspect}, #{unit_id.inspect})"
    end
  end

  # Each vector is one flag whose only rule matches its pattern against a
  # string attribute: true when the rule served, false at the fallthrough,
  # "invalid" for inconsistent_ruleset.
  def run_regex(data)
    assert_keys data, %w[name source vectors]
    vectors = data.fetch("vectors")
    refute_empty vectors
    vectors.each do |vec|
      assert_keys vec, %w[pattern input match]
      got = RegexHelper.matches(vec.fetch("pattern"), vec.fetch("input"))
      assert_equal vec.fetch("match"), got, "pattern #{vec["pattern"].inspect} on #{vec["input"].inspect}"
    end
  end

  def run_fixture(name, data)
    assert_keys data, %w[name ruleset unsupported cases]
    assert_equal name, data.fetch("name"), "fixture name does not match its path"
    rs = Flagnetic::Eval::Ruleset.decode(JSON.generate(data.fetch("ruleset")))
    assert_equal data.fetch("unsupported", {}), unsupported_report(rs), "unsupported rules"
    cases = data.fetch("cases")
    refute_empty cases
    cases.each { |c| run_case(rs, c) }
  end

  def run_case(ruleset, kase)
    assert_keys kase, %w[name context flag expect]
    context = Flagnetic::Eval::Context.new(**kase.fetch("context").transform_keys(&:to_sym))
    got = ruleset.evaluate(kase.fetch("flag"), context)
    assert_equal kase.fetch("expect"), got.as_json, "case #{kase.fetch("name").inspect}"

    # Evaluate-all memoizes prerequisites within one call; it must agree
    # with evaluating the flag on its own.
    all = ruleset.evaluate_all(context)
    return unless all.key?(kase.fetch("flag"))

    assert_equal got, all.fetch(kase.fetch("flag")), "evaluate_all disagrees, case #{kase.fetch("name").inspect}"
  end

  def unsupported_report(ruleset)
    ruleset.unsupported_rules.transform_values { |constructs| constructs.map(&:as_json) }
  end

  def assert_keys(hash, allowed)
    unknown = hash.keys - allowed
    assert_empty unknown, "unknown fixture fields"
  end
end
