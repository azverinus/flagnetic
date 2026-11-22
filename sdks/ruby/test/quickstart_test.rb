# frozen_string_literal: true

require "test_helper"

# The quickstart in docs/sdks/ruby.md is run here, block by block in one
# binding against a mock server, so that it cannot drift from the SDK: what
# it tells a reader to type works, and what it says the calls return is what
# they return.
class QuickstartTest < Minitest::Test
  include ClientHelpers

  PAGE = File.expand_path("../../../docs/sdks/ruby.md", __dir__)

  # blocks are the fenced Ruby snippets of the Quickstart section, in order.
  def blocks
    section = File.read(PAGE, encoding: "UTF-8")[/^## Quickstart\n(.*?)(?=^## )/m, 1]
    refute_nil section, "no Quickstart section in #{PAGE}"
    section.scan(/^```ruby\n(.*?)^```/m).flatten
  end

  def ruleset
    variation = ->(key) { { "key" => key, "value" => key } }
    { "schema_version" => 1, "version" => 1, "scope" => "server", "unit_types" => ["user"],
      "attributes" => [{ "key" => "plan", "type" => "string" }],
      "flags" => [
        { "key" => "new_checkout", "type" => "boolean", "on" => true,
          "variations" => [{ "key" => "yes", "value" => true }, { "key" => "no", "value" => false }],
          "off_variation" => "no", "fallthrough" => "yes" },
        { "key" => "pricing", "type" => "string", "on" => true,
          "variations" => [variation.call("standard"), variation.call("discount")],
          "off_variation" => "standard", "fallthrough" => "standard",
          "rules" => [{ "id" => "r1", "conditions" => [],
                        "serve" => { "kind" => "experiment", "experiment" => "pricing_test", "unit_type" => "user",
                                     "inclusion_salt" => "i", "variation_salt" => "v", "traffic_allocation" => 10_000,
                                     "weights" => [{ "variation" => "standard", "weight" => 5000 },
                                                   { "variation" => "discount", "weight" => 5000 }] } }] }
      ] }
  end

  def test_every_snippet_runs_and_says_what_it_returns
    @server.publish(ruleset)
    snippets = blocks
    assert_operator snippets.size, :>=, 4

    previous = ENV.to_h.slice("FLAGNETIC_SDK_KEY", "FLAGNETIC_BASE_URL")
    ENV["FLAGNETIC_SDK_KEY"] = KEY
    ENV["FLAGNETIC_BASE_URL"] = @server.base_url
    scope = binding
    snippets.each { |code| scope.eval(code) }

    client = scope.local_variable_get(:client)
    context = scope.local_variable_get(:context)
    assert_equal :fresh, client.stage
    assert_equal true, client.bool("new_checkout", default: false, context: context)
    assert_equal :fallthrough, client.detail("new_checkout", default: false, context: context).reason
    assert_includes %w[standard discount], scope.local_variable_get(:variant)
    exposures = @server.events_requests.flat_map(&:events).select { |e| e["type"] == "exposure" }
    assert_equal [["pricing_test", "pricing"]], exposures.map { |e| e.values_at("experiment_key", "flag_key") }
  ensure
    %w[FLAGNETIC_SDK_KEY FLAGNETIC_BASE_URL].each { |k| previous&.key?(k) ? ENV[k] = previous[k] : ENV.delete(k) }
  end
end
