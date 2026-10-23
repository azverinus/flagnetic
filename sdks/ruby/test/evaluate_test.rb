# frozen_string_literal: true

require "test_helper"

# EvaluateTest covers what the conformance fixtures cannot express in JSON:
# the Ruby shape of results and the refusals an SDK relies on.
class EvaluateTest < Minitest::Test
  include RulesetHelper

  Eval = Flagnetic::Eval

  def experiment_ruleset
    ruleset(flags: [
              flag("exp", [], serve: {
                     "kind" => "experiment", "experiment" => "checkout", "unit_type" => "user",
                     "inclusion_salt" => "in", "variation_salt" => "var", "traffic_allocation" => 10_000,
                     "weights" => [{ "variation" => "true", "weight" => 5000 },
                                   { "variation" => "false", "weight" => 5000 }]
                   })
            ])
  end

  def test_results_are_frozen_structs_with_symbols
    r = experiment_ruleset.evaluate("exp", { units: { user: "u1" } })
    assert_predicate r, :frozen?
    assert_kind_of Struct, r
    assert_equal :experiment, r.reason
    assert_predicate r.experiment, :frozen?
    assert_equal "checkout", r.experiment.key
    assert_equal "u1", r.experiment.unit_id
    assert_equal r.variation, r.experiment.variation
    refute r.override_influenced
    assert_raises(FrozenError) { r.experiment.key.concat("x") }
  end

  # The wire form leaves out empty strings, as the Go module's omitempty
  # encoding does: a rule without an id, a variation keyed "".
  def test_as_json_omits_empty_strings
    rs = ruleset(flags: [{ "key" => "f", "type" => "number", "on" => true, "fallthrough" => "",
                           "variations" => [{ "key" => "", "value" => 1 }],
                           "rules" => [{ "conditions" => [],
                                         "serve" => { "kind" => "variation", "variation" => "" } }] }])
    assert_equal({ "type" => "number", "value" => 1, "reason" => "rule", "automatic_exposure" => false,
                   "override_influenced" => false }, rs.evaluate("f").as_json)
  end

  def test_error_codes_are_symbols
    r = experiment_ruleset.evaluate("missing")
    assert_equal %i[error flag_not_found], [r.reason, r.error_code]
    assert_nil r.variation
    assert_nil r.value
  end

  def test_served_values_are_frozen
    rs = ruleset(flags: [{ "key" => "j", "type" => "json", "on" => false, "off_variation" => "a",
                           "variations" => [{ "key" => "a", "value" => { "list" => [1, "x"] } }] }])
    value = rs.evaluate("j").value
    assert_predicate value, :frozen?
    assert_predicate value["list"], :frozen?
    assert_predicate value["list"][1], :frozen?
  end

  def test_ruleset_is_frozen
    rs = experiment_ruleset
    assert_predicate rs, :frozen?
    assert_predicate rs.flag("exp"), :frozen?
  end

  def test_unknown_operator_is_inconsistent_ruleset
    rs = ruleset(attributes: [{ "key" => "s", "type" => "string" }],
                 flags: [flag("f",
                              [{ "kind" => "attribute", "attribute" => "s", "op" => "sounds_like", "value" => "a" }])])
    assert_inconsistent rs.evaluate("f", { attributes: { s: "a" } })
  end

  def test_unknown_rule_kind_is_inconsistent_ruleset
    rs = ruleset(flags: [flag("f", [], serve: { "kind" => "lottery", "variation" => "true" })])
    assert_inconsistent rs.evaluate("f")
  end

  def test_unknown_condition_kind_is_inconsistent_ruleset
    rs = ruleset(flags: [flag("f", [{ "kind" => "moon_phase" }])])
    assert_inconsistent rs.evaluate("f")
  end

  def test_unknown_attribute_type_is_inconsistent_ruleset
    rs = ruleset(attributes: [{ "key" => "o", "type" => "object" }],
                 flags: [flag("f", [{ "kind" => "attribute", "attribute" => "o", "op" => "exists" }])])
    assert_inconsistent rs.evaluate("f", { attributes: { o: {} } })
  end

  def test_context_may_be_omitted_or_a_hash
    rs = ruleset(attributes: [{ "key" => "plan", "type" => "string" }],
                 flags: [flag("f", [{ "kind" => "attribute", "attribute" => "plan", "op" => "eq", "value" => "pro" }])])
    assert_equal :fallthrough, rs.evaluate("f").reason
    assert_equal :rule, rs.evaluate("f", { "attributes" => { "plan" => "pro" } }).reason
    assert_equal :rule, rs.evaluate("f", Eval::Context.new(attributes: { plan: "pro" })).reason
  end

  # Strings compare byte-wise, as in the Go module, whatever encoding the
  # caller's string carries.
  def test_strings_compare_bytewise_across_encodings
    rs = ruleset(attributes: [{ "key" => "city", "type" => "string" }],
                 flags: [flag("f",
                              [{ "kind" => "attribute", "attribute" => "city", "op" => "eq", "value" => "Москва" }])])
    assert_equal :rule, rs.evaluate("f", { attributes: { city: "Москва".b } }).reason
  end

  # Neither a malformed context nor an invalid byte sequence raises:
  # evaluation never fails.
  def test_evaluation_never_raises_on_odd_contexts
    rs = ruleset(attributes: [{ "key" => "s", "type" => "string" }],
                 flags: [flag("f", [{ "kind" => "attribute", "attribute" => "s", "op" => "matches", "value" => "a" }])])
    assert_equal :rule, rs.evaluate("f", { units: { user: "\xFF".b }, attributes: { s: "\xFFa".b } }).reason
    assert_equal :fallthrough, rs.evaluate("f", { units: { user: 1 }, attributes: { s: :a } }).reason
  end

  private

  def assert_inconsistent(result)
    assert_equal %i[error inconsistent_ruleset], [result.reason, result.error_code]
  end
end
