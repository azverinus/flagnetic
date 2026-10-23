# frozen_string_literal: true

$LOAD_PATH.unshift(File.expand_path("../lib", __dir__))

require "json"
require "minitest/autorun"
require "flagnetic"

module RulesetHelper
  # ruleset decodes a server document with the given unit types, attributes
  # and flags, the way most tests need one.
  def ruleset(flags:, attributes: [], unit_types: ["user"], segments: [])
    Flagnetic::Eval::Ruleset.decode(JSON.generate(
                                      "schema_version" => 1, "version" => 1, "scope" => "server",
                                      "unit_types" => unit_types, "attributes" => attributes,
                                      "segments" => segments, "flags" => flags
                                    ))
  end

  # flag is a boolean flag serving "true" when its one rule's conditions
  # hold and "false" otherwise.
  def flag(key, conditions, serve: { "kind" => "variation", "variation" => "true" })
    {
      "key" => key, "type" => "boolean", "on" => true,
      "variations" => [{ "key" => "true", "value" => true }, { "key" => "false", "value" => false }],
      "off_variation" => "false", "fallthrough" => "false",
      "rules" => [{ "id" => "r1", "conditions" => conditions, "serve" => serve }]
    }
  end
end

# RegexHelper puts a pattern and an input through the public seam, a
# ruleset: true when the rule matched, false at the fallthrough, "invalid"
# for inconsistent_ruleset.
module RegexHelper
  extend RulesetHelper

  def self.matches(pattern, input)
    rs = ruleset(attributes: [{ "key" => "s", "type" => "string" }],
                 flags: [flag("f",
                              [{ "kind" => "attribute", "attribute" => "s", "op" => "matches", "value" => pattern }])])
    r = rs.evaluate("f", { attributes: { s: input } })
    return "invalid" if r.error_code == :inconsistent_ruleset

    r.reason == :rule
  end
end
