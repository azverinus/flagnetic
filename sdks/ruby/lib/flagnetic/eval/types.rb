# frozen_string_literal: true

module Flagnetic
  module Eval
    # Context is the subject of an evaluation: unit IDs by unit type, and
    # attributes. A context with no units is valid: attribute rules apply;
    # rollouts, experiments and overrides are skipped. An empty unit ID
    # counts as absent. Keys may be strings or symbols.
    #
    # Attribute values: String, true or false, a real Numeric, an Array of
    # strings, and for dates an RFC 3339 string, integral Unix epoch
    # milliseconds or a Time.
    Context = Struct.new(:units, :attributes, keyword_init: true) do
      def initialize(units: nil, attributes: nil)
        super(units: Context.stringify_keys(units), attributes: Context.stringify_keys(attributes))
        freeze
      end

      # unit returns the unit ID of a unit type, or nil when absent.
      def unit(unit_type)
        id = units[unit_type]
        id.is_a?(String) && !id.empty? ? Eval.utf8(id) : nil
      end

      def self.stringify_keys(hash)
        (hash || {}).transform_keys(&:to_s).freeze
      end

      # coerce accepts a Context, a Hash with units and attributes, or nil.
      def self.coerce(context)
        case context
        when Context then context
        when nil then self::EMPTY
        when Hash
          c = stringify_keys(context)
          new(units: c["units"], attributes: c["attributes"])
        else raise ArgumentError, "context must be a Flagnetic::Eval::Context or a Hash, got #{context.class}"
        end
      end
    end
    Context::EMPTY = Context.new

    # REASONS are the evaluation reasons the evaluator produces. fallback
    # and local_override are produced by SDKs, never by the evaluator.
    REASONS = %i[off override rule rollout experiment fallthrough prerequisite_failed error].freeze

    # ERROR_CODES say why a result has reason error.
    ERROR_CODES = %i[flag_not_found prerequisite_cycle inconsistent_ruleset].freeze

    # Construct is an operator, a condition kind or a serve kind of a
    # ruleset that this evaluator does not know. kind is :operator,
    # :condition_kind or :serve_kind.
    Construct = Struct.new(:kind, :name) do
      def as_json
        { "kind" => kind.to_s, "name" => name }
      end
    end

    # Result is the outcome of evaluating one flag for one context. Its
    # as_json form is what the conformance fixtures compare, key for key.
    #
    # - type: the flag's type, and automatic_exposure its setting: with it
    #   an SDK emits an exposure after every evaluation whose result carries
    #   an experiment, with no second call. Both are the flag's rather than
    #   the evaluation's and are nil only when there is no flag to read them
    #   from, an error with flag_not_found.
    # - value, variation: the served variation; nil for errors.
    # - reason: one of REASONS.
    # - rule_id: the rule that served, for reasons rule, rollout and
    #   experiment.
    # - experiment: an ExperimentDetail with reason experiment, and with
    #   reason override when the override's shadow evaluation lands in a
    #   running experiment that includes the unit.
    # - prerequisite: the flag-level prerequisite that failed, for reason
    #   prerequisite_failed.
    # - bucket: the unit's bucket for reason rollout.
    # - error_code: one of ERROR_CODES, for reason error.
    # - override_influenced: an override decided the result, or any
    #   prerequisite sub-evaluation consulted on the way was
    #   override-influenced, whatever the final reason. Every exposure and
    #   event derived from the result carries it.
    Result = Struct.new(:type, :automatic_exposure, :value, :variation, :reason, :rule_id, :experiment,
                        :prerequisite, :bucket, :error_code, :override_influenced, keyword_init: true) do
      def initialize(override_influenced: false, **fields)
        super
        freeze
      end

      # of_flag is the result stamped with the flag it is about: every step
      # leaves type and automatic_exposure alone, so one place fills them in
      # for every reason a known flag can carry.
      def of_flag(flag)
        Result.new(**to_h, type: flag.type, automatic_exposure: flag.automatic_exposure)
      end

      # as_json is the result's JSON form, keyed as the reference
      # evaluator encodes it: the flag's type and automatic exposure unless
      # there is no flag, the served value unless the reason is error, and
      # every other field unless it is unset or an empty string.
      def as_json
        out = type.nil? ? {} : { "type" => type }
        out["value"] = value unless reason == :error
        {
          "variation" => variation, "reason" => reason.to_s, "rule_id" => rule_id
        }.each { |k, v| out[k] = v unless v.nil? || v == "" }
        out["automatic_exposure"] = automatic_exposure unless type.nil?
        {
          "experiment" => experiment&.as_json, "prerequisite" => prerequisite,
          "bucket" => bucket, "error_code" => error_code&.to_s
        }.each { |k, v| out[k] = v unless v.nil? || v == "" }
        out["override_influenced"] = override_influenced
        out
      end
    end

    # ExperimentDetail is everything an SDK needs to report an exposure: the
    # one shape of experiment detail shared by local and remote evaluation.
    # variation is the served variation: the assignment, or the override's
    # variation when an override decided the result. override equals the
    # result's override_influenced; exposures carrying it are excluded from
    # analysis.
    ExperimentDetail = Struct.new(:key, :variation, :unit_type, :unit_id, :inclusion_salt, :variation_salt,
                                  :inclusion_bucket, :variation_bucket, :override, keyword_init: true) do
      def initialize(**fields)
        super
        freeze
      end

      def as_json
        to_h.transform_keys(&:to_s)
      end
    end

    # The ruleset document, as decoded. Every struct is frozen.
    Environment = Struct.new(:key, :protected, keyword_init: true)
    Attribute = Struct.new(:key, :type, keyword_init: true)
    Segment = Struct.new(:key, :private, :conditions, keyword_init: true)
    Condition = Struct.new(:kind, :attribute, :op, :value, :segment, :flag, :variation, keyword_init: true)
    Flag = Struct.new(:key, :type, :automatic_exposure, :variations, :on, :off_variation,
                      :fallthrough, :prerequisites, :overrides, :rules, keyword_init: true)
    Variation = Struct.new(:key, :value, keyword_init: true)
    Prerequisite = Struct.new(:flag, :variation, keyword_init: true)
    Override = Struct.new(:unit_type, :unit_id, :variation, keyword_init: true)
    Rule = Struct.new(:id, :conditions, :serve, keyword_init: true)
    Serve = Struct.new(:kind, :variation, :unit_type, :percentage, :salt, :experiment, :inclusion_salt,
                       :variation_salt, :traffic_allocation, :weights, keyword_init: true)
    Weight = Struct.new(:variation, :weight, keyword_init: true)
  end
end
