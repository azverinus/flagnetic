# frozen_string_literal: true

require "json"

# LargeRuleset builds a server ruleset of 1000 flags shaped like production
# ones: each flag has an override, nine in ten have a flag-level
# prerequisite on the first flag of their group of ten, and every flag has
# four rules (attribute conditions with a segment, a rollout, an experiment
# and conditions on a regular expression, a date and a list), so an
# evaluation walks every step.
module LargeRuleset
  FLAGS = 1000

  module_function

  def document
    JSON.generate(
      "schema_version" => 1, "version" => 1, "scope" => "server",
      "unit_types" => %w[user device],
      "attributes" => [{ "key" => "plan", "type" => "string" }, { "key" => "app_version", "type" => "semver" },
                       { "key" => "country", "type" => "string" }, { "key" => "age", "type" => "number" },
                       { "key" => "email", "type" => "string" }, { "key" => "signed_up", "type" => "date" },
                       { "key" => "tags", "type" => "list" }],
      "segments" => [{ "key" => "beta", "conditions" => [
        { "kind" => "attribute", "attribute" => "country", "op" => "in", "value" => %w[DE FR NL] },
        { "kind" => "attribute", "attribute" => "age", "op" => "gte", "value" => 18 }
      ] }],
      "flags" => Array.new(FLAGS) { |i| flag(i) }
    )
  end

  def flag(index)
    {
      "key" => "flag_#{index}", "type" => "boolean", "on" => true,
      "variations" => [{ "key" => "on", "value" => true }, { "key" => "off", "value" => false }],
      "off_variation" => "off", "fallthrough" => "off",
      "prerequisites" => (index % 10).zero? ? [] : [{ "flag" => "flag_#{index - (index % 10)}", "variation" => "on" }],
      "overrides" => [{ "unit_type" => "user", "unit_id" => "qa-#{index}", "variation" => "on" }],
      "rules" => [
        { "id" => "r1", "conditions" => [
          { "kind" => "attribute", "attribute" => "plan", "op" => "eq", "value" => "enterprise" },
          { "kind" => "in_segment", "segment" => "beta" }
        ], "serve" => { "kind" => "variation", "variation" => "on" } },
        { "id" => "r2", "conditions" => [
          { "kind" => "attribute", "attribute" => "app_version", "op" => "gte", "value" => "1.40.0" }
        ], "serve" => { "kind" => "rollout", "unit_type" => "user", "variation" => "on",
                        "percentage" => 9000, "salt" => "rollout-#{index}" } },
        { "id" => "r3", "conditions" => [], "serve" => {
          "kind" => "experiment", "experiment" => "exp_#{index}", "unit_type" => "user",
          "inclusion_salt" => "in-#{index}", "variation_salt" => "var-#{index}", "traffic_allocation" => 10_000,
          "weights" => [{ "variation" => "on", "weight" => 5000 }, { "variation" => "off", "weight" => 5000 }]
        } },
        { "id" => "r4", "conditions" => [
          { "kind" => "attribute", "attribute" => "email", "op" => "matches", "value" => '@(example|test)\\.com$' },
          { "kind" => "attribute", "attribute" => "signed_up", "op" => "after", "value" => "2025-01-01T00:00:00Z" },
          { "kind" => "attribute", "attribute" => "tags", "op" => "contains", "value" => "beta" }
        ], "serve" => { "kind" => "variation", "variation" => "on" } }
      ]
    }
  end

  def context(index)
    { units: { user: "u-#{index}" }, attributes: { plan: "pro", app_version: "1.41.2", country: "DE", age: 30,
                                                   email: "u#{index}@example.org", signed_up: "2025-06-01T00:00:00Z",
                                                   tags: %w[alpha beta] } }
  end
end
