# frozen_string_literal: true

$LOAD_PATH.unshift(File.expand_path("../lib", __dir__))

require "json"
require "minitest/autorun"
require "flagnetic/openfeature"

OF = OpenFeature::SDK

CONFORMANCE = File.expand_path("../../../conformance", __dir__)

# DOCUMENT exercises every mapping the provider owns: a rule (checkout), an
# off flag (disabled_flag), a fallthrough (greeting), whole and fractional
# numbers (page_size, ratio), a JSON flag (theme), a running experiment
# with certain assignment (pricing) and one opted into automatic exposure
# (auto_flag).
DOCUMENT = {
  "schema_version" => 1, "version" => 3, "scope" => "server",
  "environment" => { "key" => "staging", "protected" => false },
  "unit_types" => %w[user anonymous], "attributes" => [{ "key" => "country", "type" => "string" }],
  "segments" => [],
  "flags" => [
    { "key" => "checkout", "type" => "boolean", "automatic_exposure" => false, "on" => true,
      "variations" => [{ "key" => "on", "value" => true }, { "key" => "off", "value" => false }],
      "off_variation" => "off", "fallthrough" => "off",
      "rules" => [{ "id" => "r1", "conditions" => [{ "kind" => "attribute", "attribute" => "country", "op" => "eq",
                                                     "value" => "DE" }],
                    "serve" => { "kind" => "variation", "variation" => "on" } }] },
    { "key" => "disabled_flag", "type" => "boolean", "automatic_exposure" => false, "on" => false,
      "variations" => [{ "key" => "on", "value" => true }, { "key" => "off", "value" => false }],
      "off_variation" => "off", "fallthrough" => "on" },
    { "key" => "greeting", "type" => "string", "automatic_exposure" => false, "on" => true,
      "variations" => [{ "key" => "hello", "value" => "hello" }, { "key" => "hi", "value" => "hi" }],
      "off_variation" => "hello", "fallthrough" => "hi" },
    { "key" => "page_size", "type" => "number", "automatic_exposure" => false, "on" => true,
      "variations" => [{ "key" => "small", "value" => 10.0 }, { "key" => "large", "value" => 50 }],
      "off_variation" => "small", "fallthrough" => "small" },
    { "key" => "ratio", "type" => "number", "automatic_exposure" => false, "on" => true,
      "variations" => [{ "key" => "half", "value" => 0.5 }],
      "off_variation" => "half", "fallthrough" => "half" },
    { "key" => "theme", "type" => "json", "automatic_exposure" => false, "on" => true,
      "variations" => [{ "key" => "dark", "value" => { "mode" => "dark" } }],
      "off_variation" => "dark", "fallthrough" => "dark" },
    { "key" => "pricing", "type" => "string", "automatic_exposure" => false, "on" => true,
      "variations" => [{ "key" => "control", "value" => "old" }, { "key" => "treatment", "value" => "new" }],
      "off_variation" => "control", "fallthrough" => "control",
      "rules" => [{ "id" => "exp", "conditions" => [],
                    "serve" => { "kind" => "experiment", "experiment" => "pricing_test", "unit_type" => "user",
                                 "inclusion_salt" => "in", "variation_salt" => "var", "traffic_allocation" => 10_000,
                                 "weights" => [{ "variation" => "control", "weight" => 0 },
                                               { "variation" => "treatment", "weight" => 10_000 }] } }] },
    { "key" => "auto_flag", "type" => "string", "automatic_exposure" => true, "on" => true,
      "variations" => [{ "key" => "control", "value" => "old" }, { "key" => "treatment", "value" => "new" }],
      "off_variation" => "control", "fallthrough" => "control",
      "rules" => [{ "id" => "exp2", "conditions" => [],
                    "serve" => { "kind" => "experiment", "experiment" => "auto_test", "unit_type" => "user",
                                 "inclusion_salt" => "in2", "variation_salt" => "var2",
                                 "traffic_allocation" => 10_000,
                                 "weights" => [{ "variation" => "control", "weight" => 0 },
                                               { "variation" => "treatment", "weight" => 10_000 }] } }] }
  ]
}.freeze

QUIET = ->(_level, _event, _fields) {}

# ProviderHelper registers providers with the real OpenFeature SDK, each
# under a domain of its own, and shuts them down after the test so none
# leaks into another.
module ProviderHelper
  # of_provider builds an offline provider over DOCUMENT (or the given
  # options), registers it and returns [provider, OpenFeature client].
  def of_provider(**options)
    options = { offline: DOCUMENT, logger: QUIET }.merge(options)
    register(Flagnetic::OpenFeature::Provider.new(nil, **options))
  end

  def register(provider)
    domain = "#{self.class.name}##{name}"
    OF::API.instance.set_provider_and_wait(provider, domain: domain)
    (@of_providers ||= []) << provider
    [provider, OF::API.instance.build_client(domain: domain)]
  end

  def user(targeting_key = "u-1", **fields)
    OF::EvaluationContext.new(targeting_key: targeting_key, **fields)
  end

  def teardown
    (@of_providers || []).each(&:shutdown)
    super
  end
end
