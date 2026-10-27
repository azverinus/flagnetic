# frozen_string_literal: true

module Flagnetic
  # Eval is the evaluator; see flagnetic/eval.rb.
  module Eval
    # Values reads context and condition values as the evaluator compares
    # them.
    module Values
      module_function

      # number reads a context or condition value as a number: an Integer,
      # Float, Rational or other real Numeric, never a String ("5" is
      # wrong-typed). NaN and the infinities are not numbers.
      def number(value)
        return nil unless value.is_a?(Numeric) && value.real?

        f = value.to_f
        f.nan? || f.infinite? ? nil : f
      end

      # utf8 returns a string whose bytes compare as UTF-8, whatever encoding
      # the caller's string carries. Strings compare byte-wise.
      def utf8(str)
        str.encoding == Encoding::UTF_8 ? str : str.dup.force_encoding(Encoding::UTF_8)
      end

      # strings reads a list whose every element is a string.
      def strings(value)
        return nil unless value.is_a?(Array) && value.all?(String)

        value.map { |s| utf8(s) }
      end
    end
    private_constant :Values

    # Operators compiles an attribute condition into a matcher of a context
    # value. The matcher receives nil for an absent attribute. An absent,
    # null or wrong-typed value makes every operator false except
    # not_exists, which is then true; there is no coercion.
    module Operators
      # Error is an operator undefined for the attribute type, a condition
      # value of the wrong JSON type, or a pattern outside RE2.
      class Error < StandardError; end

      EXTRACT = {
        "string" => ->(v) { Values.utf8(v) if v.is_a?(String) },
        "number" => ->(v) { Values.number(v) },
        "boolean" => ->(v) { v if [true, false].include?(v) },
        "semver" => ->(v) { Semver.parse(v) if v.is_a?(String) },
        "date" => ->(v) { Instant.from_context(v) },
        "list" => ->(v) { Values.strings(v) }
      }.freeze
      COMPARISONS = %w[eq neq lt lte gt gte].freeze

      # KNOWN is every operator this evaluator knows, for any attribute
      # type. One outside it is an unsupported construct, typically sent by
      # a server newer than the evaluator; a known operator applied to a
      # type it is not defined for is a defect of the ruleset instead.
      KNOWN = %w[eq neq lt lte gt gte in not_in contains not_contains starts_with ends_with matches before after
                 contains_any contains_all is_empty exists not_exists].freeze
      private_constant :EXTRACT, :COMPARISONS

      module_function

      # check returns why this evaluator cannot apply op with value to an
      # attribute of type, or nil when it can: op must be defined for type
      # and value must have the JSON type op needs. It does not reject what
      # the evaluator treats as a false condition, such as an unparseable
      # semantic version or date in the value.
      def check(type, op, value)
        compile(type, op, value)
        nil
      rescue Error => e
        e.message
      end

      def compile(type, op, value)
        extract = EXTRACT[type]
        raise Error, "unknown attribute type #{type.inspect}" unless extract

        case op
        when "exists" then return ->(v) { !extract.call(v).nil? }
        when "not_exists" then return ->(v) { extract.call(v).nil? }
        end

        test = send(:"#{type}_test", op, value)
        raise Error, "operator #{op.inspect} is not defined for type #{type}" unless test

        lambda do |v|
          x = extract.call(v)
          !x.nil? && test.call(x)
        end
      end

      def string_test(op, value)
        case op
        when "eq", "neq", "contains", "not_contains", "starts_with", "ends_with"
          want = string_value(op, value)
          want_bytes = want.b
          f = case op
              when "eq", "neq" then ->(s) { s == want }
              when "contains", "not_contains" then ->(s) { s.b.include?(want_bytes) }
              when "starts_with" then ->(s) { s.b.start_with?(want_bytes) }
              else ->(s) { s.b.end_with?(want_bytes) }
              end
          negate(f, %w[neq not_contains].include?(op))
        when "in", "not_in"
          set = strings_value(op, value)
          negate(->(s) { set.include?(s) }, op == "not_in")
        when "matches"
          pattern = regex(string_value(op, value))
          ->(s) { pattern.match?(s) }
        end
      end

      def number_test(op, value)
        case op
        when *COMPARISONS
          want = json_number(value)
          raise Error, "operator #{op.inspect} needs a number value" unless want

          comparison(op) { |x| x <=> want }
        when "in", "not_in"
          set = value.map { |e| json_number(e) } if value.is_a?(Array)
          raise Error, "operator #{op.inspect} needs a list of numbers" unless set&.none?(nil)

          negate(->(x) { set.include?(x) }, op == "not_in")
        end
      end

      def boolean_test(op, value)
        return unless op == "eq"
        raise Error, "operator #{op.inspect} needs a boolean value" unless [true, false].include?(value)

        ->(x) { x == value }
      end

      def semver_test(op, value)
        case op
        when *COMPARISONS
          want = Semver.parse(string_value(op, value))
          # An unparseable version makes the condition false.
          return ->(_) { false } unless want

          comparison(op) { |x| x <=> want }
        when "in", "not_in"
          set = strings_value(op, value).map { |s| Semver.parse(s) }
          # An unparseable version makes the condition false, as it does for
          # eq and neq: skipping it instead would make not_in ["garbage"]
          # match every version.
          return ->(_) { false } if set.any?(&:nil?)

          negate(->(x) { set.any? { |v| (x <=> v).zero? } }, op == "not_in")
        end
      end

      def date_test(op, value)
        return unless %w[before after].include?(op)
        raise Error, "operator #{op.inspect} needs an RFC 3339 string" unless value.is_a?(String)

        want = Instant.parse_rfc3339(value)
        # An unparseable date makes the condition false.
        return ->(_) { false } unless want

        op == "before" ? ->(x) { x < want } : ->(x) { x > want }
      end

      def list_test(op, value)
        case op
        when "contains", "not_contains"
          want = string_value(op, value)
          negate(->(x) { x.include?(want) }, op == "not_contains")
        when "contains_any"
          wants = strings_value(op, value)
          ->(x) { wants.any? { |w| x.include?(w) } }
        when "contains_all"
          wants = strings_value(op, value)
          ->(x) { wants.all? { |w| x.include?(w) } }
        when "is_empty"
          lambda(&:empty?)
        end
      end

      # json_number reads a condition value as a number: only what JSON
      # decodes to one.
      def json_number(value)
        Values.number(value) if value.is_a?(Integer) || value.is_a?(Float)
      end

      def string_value(op, value)
        raise Error, "operator #{op.inspect} needs a string value" unless value.is_a?(String)

        Values.utf8(value)
      end

      def strings_value(op, value)
        Values.strings(value) or raise Error, "operator #{op.inspect} needs a list of strings"
      end

      def regex(pattern)
        Regex.compile(pattern)
      rescue Regex::Error => e
        raise Error, "operator \"matches\": #{e.message}"
      end

      def negate(test, negated)
        negated ? ->(x) { !test.call(x) } : test
      end

      # comparison builds eq, neq, lt, lte, gt or gte over a three-way
      # comparison of the context value against the condition value.
      def comparison(op, &cmp)
        case op
        when "eq" then ->(x) { cmp.call(x).zero? }
        when "neq" then ->(x) { !cmp.call(x).zero? }
        when "lt" then ->(x) { cmp.call(x).negative? }
        when "lte" then ->(x) { !cmp.call(x).positive? }
        when "gt" then ->(x) { cmp.call(x).positive? }
        else ->(x) { !cmp.call(x).negative? }
        end
      end
    end
    private_constant :Operators
  end
end
