# frozen_string_literal: true

require "json"

module Flagnetic
  module Eval
    # SCHEMA_VERSION is the ruleset schema version this evaluator reads.
    SCHEMA_VERSION = 1

    # SCOPE is the only scope of a ruleset this evaluator reads: the
    # document an SDK evaluates locally holds everything the environment
    # decides, private segments included. Another scope is a document made
    # for another reader.
    SCOPE = "server"

    # DecodeError is a document that is not a ruleset this evaluator can
    # read.
    class DecodeError < StandardError; end

    # Ruleset is the complete evaluable configuration of one environment at
    # one moment. Build it with Ruleset.decode. It is frozen and safe to
    # share between threads.
    class Ruleset
      attr_reader :schema_version, :version, :scope, :environment, :unit_types, :attributes, :segments, :flags

      # decode reads a ruleset document from JSON text. It raises
      # DecodeError only when the document is not a ruleset this evaluator
      # can read: malformed JSON, a field of the wrong JSON type, an
      # unsupported schema_version or a scope other than server. Unknown
      # fields are ignored and null reads as absent. Dangling references,
      # invalid conditions, invalid experiment serves and constructs this
      # evaluator does not know do not fail decoding; evaluating a flag that
      # reaches one returns reason error with inconsistent_ruleset, so one
      # defective flag never takes the others down.
      def self.decode(json)
        raise DecodeError, "decode ruleset: want JSON text, got #{json.class}" unless json.is_a?(String)

        doc = JSON.parse(Values.utf8(json), freeze: true, max_nesting: 10_000)
        new(**Document.ruleset(doc))
      rescue JSON::ParserError, EncodingError => e
        raise DecodeError, "decode ruleset: #{e.message}"
      end

      private_class_method :new

      def initialize(schema_version:, version:, scope:, environment:, unit_types:, attributes:, segments:, flags:)
        if schema_version != SCHEMA_VERSION
          raise DecodeError, "decode ruleset: unsupported schema_version #{schema_version}, " \
                             "this evaluator reads #{SCHEMA_VERSION}"
        end
        unless scope == SCOPE
          raise DecodeError, "decode ruleset: scope #{scope.inspect}, this evaluator reads #{SCOPE.inspect} only"
        end

        @schema_version = schema_version
        @version = version
        @scope = scope
        @environment = environment
        @unit_types = unit_types
        @attributes = attributes
        @segments = segments
        @flags = flags
        @index = Index.new(self)
        freeze
      end

      # protected? reports whether the ruleset belongs to a protected
      # environment. SDKs refuse local overrides there. A document without
      # an environment (a hand-written offline file) is unprotected.
      def protected?
        environment&.protected == true
      end

      # flag returns the flag with key as the document has it, or nil. SDKs
      # check a flag's type against the one requested.
      def flag(key)
        @index.flags[key]&.flag
      end

      # unsupported_rules reports, by flag key, the constructs each flag
      # holds that this evaluator does not know: an operator, a condition
      # kind or a serve kind, typically sent by a server newer than the SDK.
      # The constructs come in document order, each once; a flag holds what
      # its rules hold and what the segments its rules name hold. A flag
      # holding none is absent, so the report is empty for a ruleset this
      # evaluator fully supports.
      def unsupported_rules
        @index.unsupported
      end

      # evaluate computes one flag for a context. It never reads a clock,
      # never records an exposure and never fails: problems are results with
      # reason error.
      def evaluate(flag_key, context = nil)
        Evaluation.new(@index, Context.__send__(:coerce, context)).flag(flag_key)
      end

      # evaluate_all evaluates every flag of the ruleset for a context, keyed
      # by flag key. Prerequisite results are memoized within the call.
      def evaluate_all(context = nil)
        e = Evaluation.new(@index, Context.__send__(:coerce, context))
        flags.to_h { |f| [f.key, e.flag(f.key)] }.freeze
      end
    end

    # Document reads the decoded JSON into frozen structs, refusing a field
    # of the wrong JSON type the way the reference evaluator's typed
    # decoding does. A missing or null field is its zero value.
    module Document
      INT64 = (-(2**63))..((2**63) - 1)
      private_constant :INT64

      module_function

      def ruleset(doc)
        d = object(doc, "ruleset")
        {
          schema_version: int(d["schema_version"], "schema_version"),
          version: int(d["version"], "version"),
          scope: string(d["scope"], "scope"),
          environment: d["environment"].nil? ? nil : environment(d["environment"]),
          unit_types: list(d["unit_types"], "unit_types") { |u, p| string(u, p) },
          attributes: list(d["attributes"], "attributes") { |a, p| attribute(a, p) },
          segments: list(d["segments"], "segments") { |s, p| segment(s, p) },
          flags: list(d["flags"], "flags") { |f, p| flag(f, p) }
        }
      end

      def environment(value)
        e = object(value, "environment")
        Environment.new(key: string(e["key"], "environment.key"),
                        protected: bool(e["protected"], "environment.protected")).freeze
      end

      def attribute(value, path)
        a = object(value, path)
        Attribute.new(key: string(a["key"], "#{path}.key"), type: string(a["type"], "#{path}.type")).freeze
      end

      def segment(value, path)
        s = object(value, path)
        Segment.new(key: string(s["key"], "#{path}.key"), private: bool(s["private"], "#{path}.private"),
                    conditions: list(s["conditions"], "#{path}.conditions") { |c, p| condition(c, p) }).freeze
      end

      def condition(value, path)
        c = object(value, path)
        fields = %w[kind attribute op segment flag variation].to_h do |k|
          [k.to_sym, string(c[k], "#{path}.#{k}")]
        end
        Condition.new(value: condition_value(c["value"], "#{path}.value"), **fields).freeze
      end

      # condition_value refuses a number beyond the range of a double, which
      # the reference evaluator cannot decode into a condition value.
      # Variation values stay raw JSON there and are not checked.
      def condition_value(value, path)
        case value
        when Array then value.each_with_index { |e, i| condition_value(e, "#{path}[#{i}]") }
        when Hash then value.each { |k, v| condition_value(v, "#{path}.#{k}") }
        when Numeric then wrong(path, "a number within the range of a double", value) if value.to_f.infinite?
        end
        value
      end

      def flag(value, path)
        f = object(value, path)
        Flag.new(
          key: string(f["key"], "#{path}.key"),
          type: string(f["type"], "#{path}.type"),
          automatic_exposure: bool(f["automatic_exposure"], "#{path}.automatic_exposure"),
          variations: list(f["variations"], "#{path}.variations") { |v, p| variation(v, p) },
          on: bool(f["on"], "#{path}.on"),
          off_variation: string(f["off_variation"], "#{path}.off_variation"),
          fallthrough: string(f["fallthrough"], "#{path}.fallthrough"),
          prerequisites: list(f["prerequisites"], "#{path}.prerequisites") { |r, p| prerequisite(r, p) },
          overrides: list(f["overrides"], "#{path}.overrides") { |o, p| override(o, p) },
          rules: list(f["rules"], "#{path}.rules") { |r, p| rule(r, p) }
        ).freeze
      end

      def variation(value, path)
        v = object(value, path)
        Variation.new(key: string(v["key"], "#{path}.key"), value: v["value"]).freeze
      end

      def prerequisite(value, path)
        r = object(value, path)
        Prerequisite.new(flag: string(r["flag"], "#{path}.flag"),
                         variation: string(r["variation"], "#{path}.variation")).freeze
      end

      def override(value, path)
        o = object(value, path)
        Override.new(unit_type: string(o["unit_type"], "#{path}.unit_type"),
                     unit_id: string(o["unit_id"], "#{path}.unit_id"),
                     variation: string(o["variation"], "#{path}.variation")).freeze
      end

      def rule(value, path)
        r = object(value, path)
        Rule.new(id: string(r["id"], "#{path}.id"),
                 conditions: list(r["conditions"], "#{path}.conditions") { |c, p| condition(c, p) },
                 serve: serve(r["serve"], "#{path}.serve")).freeze
      end

      def serve(value, path)
        s = object(value, path)
        strings = %w[kind variation unit_type salt experiment inclusion_salt variation_salt].to_h do |k|
          [k.to_sym, string(s[k], "#{path}.#{k}")]
        end
        Serve.new(
          percentage: int(s["percentage"], "#{path}.percentage"),
          traffic_allocation: int(s["traffic_allocation"], "#{path}.traffic_allocation"),
          weights: list(s["weights"], "#{path}.weights") { |w, p| weight(w, p) },
          **strings
        ).freeze
      end

      def weight(value, path)
        w = object(value, path)
        Weight.new(variation: string(w["variation"], "#{path}.variation"),
                   weight: int(w["weight"], "#{path}.weight")).freeze
      end

      def object(value, path)
        return {} if value.nil?
        return value if value.is_a?(Hash)

        wrong(path, "an object", value)
      end

      def list(value, path)
        return [].freeze if value.nil?

        wrong(path, "a list", value) unless value.is_a?(Array)
        value.each_with_index.map { |e, i| yield e, "#{path}[#{i}]" }.freeze
      end

      def string(value, path)
        return "" if value.nil?
        return value if value.is_a?(String)

        wrong(path, "a string", value)
      end

      def bool(value, path)
        return false if value.nil?
        return value if [true, false].include?(value)

        wrong(path, "a boolean", value)
      end

      def int(value, path)
        return 0 if value.nil?
        return value if value.is_a?(Integer) && INT64.cover?(value)

        wrong(path, "an integer", value)
      end

      def wrong(path, want, value)
        raise DecodeError, "decode ruleset: #{path} must be #{want}, got #{JSON.generate(value, allow_nan: true)}"
      end
    end
    private_constant :Document
  end
end
