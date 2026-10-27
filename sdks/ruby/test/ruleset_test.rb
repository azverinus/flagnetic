# frozen_string_literal: true

require "test_helper"

# RulesetTest mirrors the Go module's eval/ruleset_test.go.
class RulesetTest < Minitest::Test
  include RulesetHelper

  Eval = Flagnetic::Eval

  def test_decode_refuses_what_it_cannot_read
    {
      "malformed JSON" => '{"schema_version":1,',
      "missing schema_version" => '{"version":1,"scope":"server"}',
      "future schema_version" => '{"schema_version":2,"version":1,"scope":"server"}',
      "unknown scope" => '{"schema_version":1,"version":1,"scope":"edge"}',
      "flags of the wrong JSON type" => '{"schema_version":1,"version":1,"scope":"server","flags":{}}',
      # The Go module decodes into typed fields, so every field of the wrong
      # JSON type refuses the document; this evaluator checks the same shape.
      "not an object" => "[]",
      "schema_version as a string" => '{"schema_version":"1","version":1,"scope":"server"}',
      "fractional version" => '{"schema_version":1,"version":1.5,"scope":"server"}',
      "unit type not a string" => '{"schema_version":1,"version":1,"scope":"server","unit_types":[1]}',
      "flag not an object" => '{"schema_version":1,"version":1,"scope":"server","flags":[true]}',
      "on as a string" => '{"schema_version":1,"version":1,"scope":"server","flags":[{"key":"f","on":"yes"}]}',
      "fractional percentage" => '{"schema_version":1,"version":1,"scope":"server","flags":[{"key":"f",' \
                                 '"rules":[{"id":"r","conditions":[],"serve":{"kind":"rollout","percentage":50.5}}]}]}',
      "weights not a list" => '{"schema_version":1,"version":1,"scope":"server","flags":[{"key":"f",' \
                              '"rules":[{"id":"r","serve":{"kind":"experiment","weights":{}}}]}]}',
      "environment not an object" => '{"schema_version":1,"version":1,"scope":"server","environment":"prod"}',
      "condition value beyond a double" => '{"schema_version":1,"version":1,"scope":"server","flags":[{"key":"f",' \
                                           '"rules":[{"conditions":[{"kind":"attribute","value":[1e400]}]}]}]}',
      "not JSON text" => 42
    }.each do |name, doc|
      assert_raises(Eval::DecodeError, name) { Eval::Ruleset.decode(doc) }
    end
  end

  def test_decode_tolerates_unknown_fields_and_nulls
    rs = Eval::Ruleset.decode('{"schema_version":1,"version":7,"scope":"server","later":{"x":1},' \
                              '"unit_types":null,"flags":[{"key":"f","type":"boolean","on":false,"rules":null,' \
                              '"off_variation":"false","variations":[{"key":"false","value":false}]}]}')
    assert_equal 7, rs.version
    assert_equal "server", rs.scope
    assert_equal :off, rs.evaluate("f").reason
  end

  # A variation value is raw JSON in the Go module and never read as a
  # number, so it decodes whatever its magnitude.
  def test_variation_value_beyond_a_double_decodes
    rs = Eval::Ruleset.decode('{"schema_version":1,"version":1,"scope":"server","flags":[{"key":"f",' \
                              '"off_variation":"a","variations":[{"key":"a","value":1e400}]}]}')
    assert_equal :off, rs.evaluate("f").reason
  end

  def test_protected_environment
    {
      '{"schema_version":1,"version":1,"scope":"server","environment":{"key":"production","protected":true}}' => true,
      '{"schema_version":1,"version":1,"scope":"server","environment":{"key":"staging","protected":false}}' => false,
      '{"schema_version":1,"version":1,"scope":"server"}' => false
    }.each do |doc, want|
      assert_equal want, Eval::Ruleset.decode(doc).protected?, doc
    end
  end

  def test_flag_looks_a_flag_up_by_key
    rs = Eval::Ruleset.decode('{"schema_version":1,"version":1,"scope":"server","flags":[
      {"key":"copy","type":"string","automatic_exposure":true,"on":false,
       "variations":[{"key":"a","value":"A"}],"off_variation":"a"}]}')
    f = rs.flag("copy")
    assert_equal ["copy", "string", true], [f.key, f.type, f.automatic_exposure]
    assert_nil rs.flag("nope")
  end

  # A Ruby caller passes native values, not only what JSON.parse produces.
  def test_context_accepts_ruby_values
    rs = ruleset(
      attributes: [{ "key" => "n", "type" => "number" }, { "key" => "l", "type" => "list" },
                   { "key" => "d", "type" => "date" }],
      flags: [{
        "key" => "f", "type" => "string", "on" => true, "off_variation" => "off", "fallthrough" => "off",
        "variations" => [{ "key" => "off", "value" => "off" }, { "key" => "on", "value" => { "copy" => "Try free" } }],
        "rules" => [{ "id" => "r1", "conditions" => [
          { "kind" => "attribute", "attribute" => "n", "op" => "gte", "value" => 10 },
          { "kind" => "attribute", "attribute" => "l", "op" => "contains", "value" => "beta" },
          { "kind" => "attribute", "attribute" => "d", "op" => "before", "value" => "2026-09-19T12:00:00Z" }
        ], "serve" => { "kind" => "variation", "variation" => "on" } }]
      }]
    )
    noon = Time.utc(2026, 9, 19, 12)
    {
      "Integer, Array, Time" => { n: 10, l: ["beta"], d: noon - 1 },
      "Float, symbol keys, epoch milliseconds" => { n: 11.0, l: %w[alpha beta], d: (noon.to_i * 1000) - 1 },
      "Rational, string keys, RFC 3339" => { "n" => Rational(25, 2), "l" => ["beta"], "d" => "2026-09-19T11:00:00Z" }
    }.each do |name, attrs|
      r = rs.evaluate("f", { attributes: attrs })
      assert_equal [:rule, "on", { "copy" => "Try free" }], [r.reason, r.variation, r.value], name
    end
  end

  def test_evaluate_all_covers_every_flag
    rs = Eval::Ruleset.decode('{"schema_version":1,"version":1,"scope":"server","unit_types":[],
      "flags":[{"key":"a","type":"boolean","on":false,"off_variation":"false",
        "variations":[{"key":"true","value":true},{"key":"false","value":false}]},
        {"key":"b","type":"boolean","on":false,"off_variation":"true",
        "variations":[{"key":"true","value":true}]}]}')
    all = rs.evaluate_all(Eval::Context.new)
    assert_equal %w[a b], all.keys.sort
    assert_equal :off, all["a"].reason
    assert_equal [:off, "true"], [all["b"].reason, all["b"].variation]
  end

  # A document for another reader (a client key's) is not one this
  # evaluator may serve a server key's values from.
  def test_a_scope_other_than_server_is_refused
    %w[client later].each do |scope|
      error = assert_raises(Eval::DecodeError) do
        Eval::Ruleset.decode(%({"schema_version":1,"version":1,"scope":"#{scope}"}))
      end
      assert_match(/scope "#{scope}"/, error.message)
    end
  end

  def test_a_ruleset_this_evaluator_fully_supports_reports_nothing_unsupported
    rs = Eval::Ruleset.decode('{"schema_version":1,"version":1,"scope":"server","flags":[{"key":"f","on":false}]}')
    assert_empty rs.unsupported_rules
  end

  # A condition with an empty segment key names no segment, so a segment
  # whose key is empty lends it none of its unsupported constructs, as in
  # the Go module.
  def test_an_empty_segment_key_names_no_segment_in_the_unsupported_report
    rs = Eval::Ruleset.decode('{"schema_version":1,"version":1,"scope":"server",
      "segments":[{"key":"","conditions":[{"kind":"attribute","attribute":"a","op":"sounds_like","value":"x"}]}],
      "flags":[{"key":"f","on":true,
        "rules":[{"id":"r","conditions":[{"kind":"in_segment","segment":""}],"serve":{"kind":"variation"}}]}]}')
    assert_empty rs.unsupported_rules
  end

  def test_the_operator_check_follows_the_operator_table
    [
      ["string", "matches", "^a", true],
      ["string", "matches", "(?=a)", false],
      ["string", "lt", "a", false],
      ["string", "in", %w[a b], true],
      ["string", "in", ["a", 1.0], false],
      ["number", "gte", 1.5, true],
      ["number", "gte", "1.5", false],
      ["boolean", "eq", false, true],
      ["boolean", "neq", false, false],
      ["semver", "lt", "1.41", true],
      ["date", "before", "2026-01-01T00:00:00Z", true],
      ["date", "eq", "2026-01-01T00:00:00Z", false],
      ["list", "contains_all", ["a"], true],
      ["list", "is_empty", nil, true],
      ["string", "is_empty", nil, false],
      ["string", "exists", nil, true],
      ["object", "exists", nil, false]
    ].each do |type, op, value, ok|
      problem = Eval.const_get(:Operators).check(type, op, value)
      assert_equal ok, problem.nil?, "check(#{type}, #{op}, #{value.inspect}) = #{problem.inspect}"
    end
  end
end
