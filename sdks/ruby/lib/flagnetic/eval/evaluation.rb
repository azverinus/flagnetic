# frozen_string_literal: true

module Flagnetic
  # Eval is the evaluator; see flagnetic/eval.rb.
  module Eval
    # Index holds the flag and segment maps and the conditions of a decoded
    # ruleset, compiled once, and what the ruleset holds that this evaluator
    # does not know.
    class Index
      # invalid_serves marks, by rule index, the experiment serves that
      # break the invariants every experiment serve holds (see
      # Compiler#valid_experiment?), so evaluation reaching one answers
      # inconsistent_ruleset without checking them again.
      CompiledFlag = Struct.new(:flag, :variations, :rules, :invalid_serves)
      # CompiledCondition is a condition prepared once at decode time. error
      # is set when it cannot be evaluated against this document; evaluating
      # it then yields inconsistent_ruleset. unsupported is the construct of
      # an error that comes from an operator or condition kind this
      # evaluator does not know.
      CompiledCondition = Struct.new(:kind, :error, :attribute, :names_unit, :match, :segment, :flag, :variation,
                                     :unsupported)
      # CompiledSegment is a segment's conditions and the unsupported
      # constructs it holds.
      CompiledSegment = Struct.new(:conditions, :unsupported)

      attr_reader :flags, :segments, :unsupported

      def initialize(ruleset)
        # A unit type named as an attribute is a string attribute holding
        # that unit's ID.
        units = ruleset.unit_types.to_h { |u| [u, true] }
        types = ruleset.attributes.to_h { |a| [a.key, a.type] }.merge(units.transform_values { "string" })
        compiler = Compiler.new(types, units)
        @segments = ruleset.segments.to_h { |s| [s.key, compiler.segment(s)] }.freeze
        @flags = {}
        @unsupported = {}
        ruleset.flags.each do |f|
          @flags[f.key] = compiler.flag(f)
          found = compiler.unsupported_in(f, @flags[f.key], @segments)
          @unsupported[f.key] = found.freeze unless found.empty?
        end
        @flags.freeze
        @unsupported.freeze
        freeze
      end
    end

    # The condition kinds and serve kinds this evaluator knows.
    CONDITION_KINDS = %w[attribute in_segment not_in_segment prerequisite].freeze
    SERVE_KINDS = %w[variation rollout experiment].freeze
    private_constant :CONDITION_KINDS, :SERVE_KINDS

    # Compiler compiles the conditions of one ruleset against its declared
    # attribute types and unit types.
    Compiler = Struct.new(:types, :units) do
      def segment(segment)
        found = []
        conditions = segment.conditions.map do |c|
          next add_unsupported(found, compile(c)) if c.kind == "attribute"

          error = "segment #{segment.key.inspect} has a #{c.kind.inspect} condition"
          unknown = construct(:condition_kind, c.kind) unless CONDITION_KINDS.include?(c.kind)
          add_unsupported(found, invalid(c, error, unknown))
        end
        Index::CompiledSegment.new(conditions.freeze, found.freeze).freeze
      end

      def flag(flag)
        variations = flag.variations.to_h { |v| [v.key, v.value] }.freeze
        rules = flag.rules.map { |r| r.conditions.map { |c| compile(c) }.freeze }.freeze
        invalid_serves = flag.rules.map { |r| r.serve.kind == "experiment" && !valid_experiment?(r.serve, variations) }
        Index::CompiledFlag.new(flag, variations, rules, invalid_serves.freeze).freeze
      end

      # unsupported_in lists the constructs a compiled flag holds that this
      # evaluator does not know, in document order, each once: those of its
      # rules, and of the segments its rules name.
      def unsupported_in(flag, compiled, segments)
        found = []
        flag.rules.each_with_index do |rule, i|
          compiled.rules[i].each do |c|
            add_unsupported(found, c)
            next if c.segment.to_s.empty?

            segments[c.segment]&.unsupported&.each { |u| found << u unless found.include?(u) }
          end
          kind = rule.serve.kind
          add_construct(found, construct(:serve_kind, kind)) unless SERVE_KINDS.include?(kind)
        end
        found
      end

      private

      # valid_experiment? reports whether an experiment serve holds what
      # assigning a unit to a variation relies on: an experiment key, a unit
      # type and both salts; a traffic allocation within 0 to 10000; and at
      # least two weights, none negative, each naming a distinct variation
      # of the flag, summing to exactly 10000. Any other serve would assign
      # units to variations the experiment does not compare, or to none, so
      # every evaluator refuses it the same way instead of each assigning
      # its own way.
      def valid_experiment?(serve, variations)
        return false if [serve.experiment, serve.unit_type, serve.inclusion_salt, serve.variation_salt].any?(&:empty?)
        return false unless (0..BUCKETS).cover?(serve.traffic_allocation) && serve.weights.size >= 2

        named = serve.weights.map(&:variation)
        named.uniq.size == named.size && named.all? { |v| variations.key?(v) } &&
          serve.weights.none? { |w| w.weight.negative? } && serve.weights.sum(&:weight) == BUCKETS
      end

      def add_unsupported(found, compiled)
        add_construct(found, compiled.unsupported)
        compiled
      end

      def add_construct(found, construct)
        found << construct if construct && !found.include?(construct)
      end

      # construct is the unsupported construct of a kind with a name; none
      # for a missing name: no server sends that, so it is a defect of the
      # ruleset rather than a reason to upgrade.
      def construct(kind, name)
        Construct.new(kind, name).freeze unless name.empty?
      end

      def invalid(cond, error, unsupported = nil)
        Index::CompiledCondition.new(cond.kind, error, nil, nil, nil, nil, nil, nil, unsupported).freeze
      end

      def compile(cond)
        case cond.kind
        when "attribute" then compile_attribute(cond)
        when "in_segment", "not_in_segment"
          Index::CompiledCondition.new(cond.kind, nil, nil, false, nil, cond.segment).freeze
        when "prerequisite"
          Index::CompiledCondition.new(cond.kind, nil, nil, false, nil, nil, cond.flag, cond.variation).freeze
        else
          invalid(cond, "unknown condition kind #{cond.kind.inspect}", construct(:condition_kind, cond.kind))
        end
      end

      def compile_attribute(cond)
        unless Operators::KNOWN.include?(cond.op)
          return invalid(cond, "unknown operator #{cond.op.inspect}", construct(:operator, cond.op))
        end

        type = types[cond.attribute]
        return invalid(cond, "attribute #{cond.attribute.inspect} is not declared") unless type

        match = Operators.compile(type, cond.op, cond.value)
        Index::CompiledCondition.new(cond.kind, nil, cond.attribute, units.key?(cond.attribute), match).freeze
      rescue Operators::Error => e
        invalid(cond, "attribute #{cond.attribute.inspect}: #{e.message}")
      end
    end
    private_constant :Index, :Compiler

    # Evaluation is the state of one evaluate or evaluate_all call: the
    # flags on the current prerequisite path, and every flag result
    # computed so far.
    #
    # For one flag and one context:
    #
    # 1. An absent flag is error flag_not_found.
    # 2. A flag that is off serves its off variation, reason off.
    # 3. The first override whose unit type is in the context with an equal
    #    unit ID serves its variation, reason override, override_influenced
    #    true. Steps 4 and 5 then run in shadow, ignoring the override; when
    #    they end in an experiment that includes the unit, the result
    #    carries that experiment's detail with override true and the served
    #    variation.
    # 4. Flag-level prerequisites in order: each evaluates the prerequisite
    #    flag with the same context and holds when its reason is neither off
    #    nor error and it serves the required variation. The first failure
    #    serves the off variation, reason prerequisite_failed, naming that
    #    prerequisite. A flag met again on the current prerequisite path is
    #    error prerequisite_cycle. An error in a prerequisite becomes the
    #    dependent flag's error.
    # 5. Rules in order, first match wins; a rule's conditions in order,
    #    stopping at the first false one. A rollout or experiment whose unit
    #    type is absent, or whose unit falls outside it, lets evaluation
    #    continue with the next rule.
    # 6. No rule matched: the fallthrough variation, reason fallthrough.
    # 7. A variation, segment, flag or attribute referenced but missing from
    #    the document, or a condition that cannot be evaluated, is error
    #    inconsistent_ruleset, only when evaluation reaches it.
    class Evaluation
      # Failure carries an error code out of nested condition evaluation.
      Failure = Struct.new(:code)
      private_constant :Failure

      def initialize(index, context)
        @index = index
        @context = context
        @visiting = {}
        @memo = {}
      end

      # flag evaluates a flag by key. A flag reached again while it is on
      # the current prerequisite path is a cycle.
      def flag(key)
        return @memo[key] if @memo.key?(key)

        f = @index.flags[key]
        return error(:flag_not_found) unless f
        return error(:prerequisite_cycle).of_flag(f.flag) if @visiting.key?(key)

        @visiting[key] = true
        result = compute(f).of_flag(f.flag)
        @visiting.delete(key)
        @memo[key] = result
      end

      private

      def error(code, influenced = false) # rubocop:disable Style/OptionalBooleanParameter
        Result.new(reason: :error, error_code: code, override_influenced: influenced)
      end

      def compute(cf)
        f = cf.flag
        return serve(cf, f.off_variation, reason: :off) unless f.on

        f.overrides.each do |o|
          next unless @context.unit(o.unit_type) == o.unit_id

          r = serve(cf, o.variation, reason: :override, override_influenced: true)
          return r if r.reason == :error

          shadow = body(cf)
          return r unless shadow.reason == :experiment

          x = shadow.experiment.to_h.merge(variation: r.variation, override: true)
          return Result.new(**r.to_h, experiment: ExperimentDetail.new(**x))
        end
        body(cf)
      end

      # body runs steps 4 to 6: prerequisites, rules and the fallthrough.
      def body(cf)
        f = cf.flag
        influenced = [false]

        f.prerequisites.each do |p|
          held = prerequisite(p.flag, p.variation, influenced)
          return error(held.code, influenced[0]) if held.is_a?(Failure)
          next if held

          return serve(cf, f.off_variation, reason: :prerequisite_failed, prerequisite: p.flag,
                                            override_influenced: influenced[0])
        end

        f.rules.each_with_index do |rule, i|
          matched = conditions(cf.rules[i], influenced)
          return error(matched.code, influenced[0]) if matched.is_a?(Failure)
          next unless matched
          return error(:inconsistent_ruleset, influenced[0]) if cf.invalid_serves[i]

          r = serve_rule(cf, rule, influenced[0])
          return r if r
        end

        serve(cf, f.fallthrough, reason: :fallthrough, override_influenced: influenced[0])
      end

      # serve_rule serves a matching rule, or returns nil when a rollout or
      # experiment leaves the unit out and evaluation continues.
      def serve_rule(cf, rule, influenced)
        s = rule.serve
        case s.kind
        when "variation"
          serve(cf, s.variation, reason: :rule, rule_id: rule.id, override_influenced: influenced)
        when "rollout"
          id = @context.unit(s.unit_type)
          bucket = id && Eval.bucket(s.salt, id)
          return nil unless bucket && bucket < s.percentage

          serve(cf, s.variation, reason: :rollout, rule_id: rule.id, bucket: bucket, override_influenced: influenced)
        when "experiment"
          experiment(cf, rule, influenced)
        else
          error(:inconsistent_ruleset, influenced)
        end
      end

      def experiment(cf, rule, influenced)
        s = rule.serve
        id = @context.unit(s.unit_type)
        return nil unless id

        inclusion = Eval.bucket(s.inclusion_salt, id)
        return nil unless inclusion < s.traffic_allocation

        variation_bucket = Eval.bucket(s.variation_salt, id)
        assigned = assign(s.weights, variation_bucket)
        return error(:inconsistent_ruleset, influenced) unless assigned

        r = serve(cf, assigned, reason: :experiment, rule_id: rule.id, override_influenced: influenced)
        return r if r.reason == :error

        Result.new(**r.to_h, experiment: ExperimentDetail.new(
          key: s.experiment, variation: assigned, unit_type: s.unit_type, unit_id: id,
          inclusion_salt: s.inclusion_salt, variation_salt: s.variation_salt,
          inclusion_bucket: inclusion, variation_bucket: variation_bucket, override: influenced
        ))
      end

      # assign walks weights in list order; variation i covers
      # [sum(w_0..w_{i-1}), sum(w_0..w_i)).
      def assign(weights, bucket)
        upper = 0
        weights.each do |w|
          upper += w.weight
          return w.variation if bucket < upper
        end
        nil
      end

      # serve builds a result serving a variation of the flag, or
      # inconsistent_ruleset when the variation is missing (step 7).
      def serve(cf, variation, **fields)
        unless cf.variations.key?(variation)
          return error(:inconsistent_ruleset,
                       fields.fetch(:override_influenced, false))
        end

        Result.new(value: cf.variations[variation], variation: variation, **fields)
      end

      # prerequisite evaluates another flag for the same context. It holds
      # when that flag's reason is neither off nor error and it serves the
      # required variation. An error in the prerequisite is a defect of the
      # ruleset and is returned as a Failure, so it surfaces on the
      # dependent flag instead of silently serving its off variation.
      # Influence accumulates into influenced[0].
      def prerequisite(key, variation, influenced)
        f = @index.flags[key]
        return Failure.new(:inconsistent_ruleset) if f.nil? || !f.variations.key?(variation)

        r = flag(key)
        influenced[0] ||= r.override_influenced
        return Failure.new(r.error_code) if r.reason == :error

        r.reason != :off && r.variation == variation
      end

      # conditions evaluates an AND-set in order and stops at the first false
      # condition; later conditions are not evaluated and cannot influence
      # the result.
      def conditions(conds, influenced)
        conds.each do |c|
          held = condition(c, influenced)
          return held if held.is_a?(Failure) || !held
        end
        true
      end

      def condition(cond, influenced)
        return Failure.new(:inconsistent_ruleset) if cond.error

        case cond.kind
        when "attribute"
          value = cond.names_unit ? @context.unit(cond.attribute) : @context.attributes[cond.attribute]
          cond.match.call(value)
        when "in_segment", "not_in_segment"
          segment = @index.segments[cond.segment]
          return Failure.new(:inconsistent_ruleset) unless segment

          inside = conditions(segment.conditions, influenced)
          return inside if inside.is_a?(Failure)

          inside == (cond.kind == "in_segment")
        else
          prerequisite(cond.flag, cond.variation, influenced)
        end
      end
    end
    private_constant :Evaluation
  end
end
