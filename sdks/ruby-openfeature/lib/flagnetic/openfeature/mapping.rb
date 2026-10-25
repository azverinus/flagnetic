# frozen_string_literal: true

module Flagnetic
  # The mapping from Flagnetic to OpenFeature every Flagnetic provider
  # shares: reasons, error codes, flag metadata and the evaluation context.
  module OpenFeature
    # REASON_METADATA_KEY is the flag metadata key carrying the Flagnetic
    # evaluation reason, which OpenFeature's coarser reason cannot tell
    # apart: a rule and a failed prerequisite are both TARGETING_MATCH.
    REASON_METADATA_KEY = "flagnetic_reason"

    # reason_of returns the OpenFeature reason of a Flagnetic evaluation
    # reason (a Symbol or String). A reason a newer server adds is UNKNOWN
    # rather than passed off as a known one; the Flagnetic reason still
    # travels in the flag metadata.
    def self.reason_of(reason)
      Mapping::REASONS.fetch(reason.to_s, ::OpenFeature::SDK::Provider::Reason::UNKNOWN)
    end

    # Mapping turns what the client serves into what OpenFeature expects;
    # Provider is its one user.
    module Mapping
      # REASONS maps every Flagnetic evaluation reason to its OpenFeature
      # reason, identically in every provider of every language, so
      # dashboards agree across services. targeting_match is what a client
      # key is told in place of the reasons targeting decided, and error is
      # the evaluator's reason a getter turns into fallback.
      REASONS = {
        "rule" => ::OpenFeature::SDK::Provider::Reason::TARGETING_MATCH,
        "prerequisite_failed" => ::OpenFeature::SDK::Provider::Reason::TARGETING_MATCH,
        "targeting_match" => ::OpenFeature::SDK::Provider::Reason::TARGETING_MATCH,
        "override" => ::OpenFeature::SDK::Provider::Reason::TARGETING_MATCH,
        "rollout" => ::OpenFeature::SDK::Provider::Reason::SPLIT,
        "experiment" => ::OpenFeature::SDK::Provider::Reason::SPLIT,
        "fallthrough" => ::OpenFeature::SDK::Provider::Reason::DEFAULT,
        "off" => ::OpenFeature::SDK::Provider::Reason::DISABLED,
        "error" => ::OpenFeature::SDK::Provider::Reason::ERROR,
        "fallback" => ::OpenFeature::SDK::Provider::Reason::ERROR,
        "local_override" => ::OpenFeature::SDK::Provider::Reason::STATIC
      }.freeze

      # ERROR_CODES names the two Flagnetic error codes OpenFeature has an
      # identity for; every other reason for a fallback is GENERAL.
      ERROR_CODES = {
        flag_not_found: ::OpenFeature::SDK::Provider::ErrorCode::FLAG_NOT_FOUND,
        type_mismatch: ::OpenFeature::SDK::Provider::ErrorCode::TYPE_MISMATCH
      }.freeze

      # The attribute of an evaluation context carrying further unit IDs by
      # unit type, beside the targeting key.
      UNITS_ATTRIBUTE = "units"

      module_function

      # resolution turns a served Flagnetic::Detail into OpenFeature
      # ResolutionDetails: a fallback is an ERROR carrying the mapped error
      # code and the Flagnetic one as its message, anything else the mapped
      # reason with its variation as the variant.
      def resolution(detail)
        if detail.reason == :fallback
          return ::OpenFeature::SDK::Provider::ResolutionDetails.new(
            value: detail.value, reason: ::OpenFeature::SDK::Provider::Reason::ERROR,
            error_code: ERROR_CODES.fetch(detail.error_code, ::OpenFeature::SDK::Provider::ErrorCode::GENERAL),
            error_message: detail.error_code.to_s, flag_metadata: flag_metadata(detail)
          )
        end

        ::OpenFeature::SDK::Provider::ResolutionDetails.new(
          value: detail.value, variant: detail.variation, reason: OpenFeature.reason_of(detail.reason),
          flag_metadata: flag_metadata(detail)
        )
      end

      # flag_metadata carries a detail's Flagnetic reason, stage, ruleset
      # version, variation and platform override, and its experiment block
      # without the salts, flattened to the scalar values flag metadata
      # holds. What the detail has none of is left out: a scalar has no
      # value meaning absent.
      def flag_metadata(detail)
        metadata = { REASON_METADATA_KEY => detail.reason.to_s, "stage" => detail.stage.to_s,
                     "ruleset_version" => detail.ruleset_version, "variation" => detail.variation,
                     "overridden" => detail.overridden }.compact
        experiment = detail.experiment
        return metadata unless experiment

        metadata.merge(
          "experiment_key" => experiment.key, "experiment_variation" => experiment.variation,
          "experiment_unit_type" => experiment.unit_type, "experiment_unit_id" => experiment.unit_id,
          "experiment_inclusion_bucket" => experiment.inclusion_bucket,
          "experiment_variation_bucket" => experiment.variation_bucket, "experiment_override" => experiment.override
        )
      end

      # context turns an OpenFeature EvaluationContext, or nil, into a
      # Flagnetic context: the targeting key is the unit ID of unit_type,
      # the units attribute (a Hash of unit type to unit ID) adds further
      # units, and every other attribute is an attribute.
      def context(evaluation_context, unit_type)
        units = {}
        attributes = {}
        evaluation_context&.fields&.each do |key, value|
          next if key == ::OpenFeature::SDK::EvaluationContext::TARGETING_KEY

          if key == UNITS_ATTRIBUTE
            units.merge!(unit_ids(value))
          else
            attributes[key] = value
          end
        end
        targeting_key = evaluation_context&.targeting_key
        units[unit_type] = targeting_key if targeting_key.is_a?(String) && !targeting_key.empty?
        { units: units, attributes: attributes }
      end

      def unit_ids(value)
        return {} unless value.is_a?(Hash)

        value.each_with_object({}) do |(unit_type, unit_id), out|
          out[unit_type.to_s] = unit_id if unit_id.is_a?(String)
        end
      end
    end
    private_constant :Mapping
  end
end
