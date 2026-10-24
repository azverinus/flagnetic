# frozen_string_literal: true

module Flagnetic
  # REASONS are the evaluation reasons a Detail carries. The evaluator
  # produces the first eight; the SDK produces local_override and
  # fallback. A getter never serves error: an evaluator error serves the
  # built-in default with reason fallback.
  REASONS = %i[rule rollout experiment override prerequisite_failed error off fallthrough local_override
               fallback].freeze

  # STAGES are the steps of the fallback chain. built_in: no ruleset was
  # ever received, getters serve built-in defaults. last_good: the held
  # ruleset came from an earlier success, or from the store, and the most
  # recent attempt failed. fresh: the most recent attempt succeeded, a 304
  # included. An offline client is always fresh once its document loads.
  STAGES = %i[built_in last_good fresh].freeze

  # ERROR_CODES say why a Detail has reason fallback: the evaluator's four,
  # then type_mismatch (the flag's type differs from the getter's),
  # no_ruleset (stage built_in) and general (an internal failure caught at
  # the API boundary).
  ERROR_CODES = %i[flag_not_found server_only prerequisite_cycle inconsistent_ruleset type_mismatch no_ruleset
                   general].freeze

  # Detail is a served value and why it was served.
  #
  # - value: the served value (true or false, a String, a Numeric, or for a
  #   JSON flag the parsed, frozen value), or the built-in default as passed
  #   with reason fallback.
  # - reason: one of REASONS. variation and rule_id as the evaluator sets
  #   them; error_code with reason fallback.
  # - ruleset_version, received_at: the ruleset evaluated against, nil at
  #   stage built_in. The age of a last good ruleset is observable through
  #   received_at.
  # - stage: one of STAGES, at the moment of the evaluation.
  # - overridden: a platform override decided the value, directly or
  #   through a prerequisite. A local override is reason local_override
  #   instead.
  # - experiment: an Eval::ExperimentDetail when the value came from a
  #   running experiment, an override landing in one included.
  Detail = Struct.new(:value, :reason, :variation, :rule_id, :error_code, :ruleset_version, :received_at, :stage,
                      :overridden, :experiment, keyword_init: true) do
    def initialize(overridden: false, **fields)
      super
      freeze
    end
  end

  # Status is the state of the fallback chain: the stage, and since when
  # the held ruleset is stale (the time of the first failed attempt since
  # the last success), nil while the most recent attempt succeeded.
  Status = Struct.new(:stage, :stale_since, keyword_init: true) do
    def initialize(**fields)
      super
      freeze
    end
  end

  # Error is the base of every error the SDK raises. Getters never raise.
  class Error < StandardError; end

  # LocalOverridesNotAllowed is raised by LocalOverrides#set on a client
  # constructed without allow_overrides: true.
  class LocalOverridesNotAllowed < Error; end

  # LocalOverrideRefused is raised by LocalOverrides#set when the held
  # ruleset belongs to a protected environment.
  class LocalOverrideRefused < Error; end

  # OfflineError is raised by Client#reload when the offline document
  # cannot be read or decoded; the held ruleset stays.
  class OfflineError < Error; end
end
