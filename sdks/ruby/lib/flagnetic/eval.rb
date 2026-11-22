# frozen_string_literal: true

require_relative "eval/murmur3"
require_relative "eval/semver"
require_relative "eval/instant"
require_relative "eval/regex"
require_relative "eval/operators"
require_relative "eval/types"
require_relative "eval/ruleset"
require_relative "eval/evaluation"

module Flagnetic
  # Eval is the Flagnetic evaluator: a pure function of a ruleset document
  # and a context that computes a flag's value and evaluation reason.
  #
  # It mirrors the reference evaluator, the Go module in eval/, and passes
  # the same language-agnostic conformance suite in conformance/fixtures.
  # It has no dependencies outside the standard library, never reads a
  # clock, never performs I/O and never records an exposure: exposure is an
  # explicit SDK call, so being assigned a variation is never mistaken for
  # having seen it.
  #
  #   ruleset = Flagnetic::Eval::Ruleset.decode(document)
  #   result = ruleset.evaluate("new_matching", { units: { user: "u-1001" },
  #                                               attributes: { app_version: "1.42.0" } })
  #   result.reason # => :rule
  #
  # Operators: an absent attribute, nil, or a value of the wrong type makes
  # every operator false except not_exists, which is then true. There is no
  # coercion: "5" is not a number. An unparsable semver or date in a context
  # is a value of the wrong type. In a condition value it makes the
  # condition false, and so does one unparsable item of an in or not_in
  # list. Strings compare byte-wise; numbers are IEEE 754
  # doubles; regular expressions follow the policy in Regex. A unit type
  # named as an attribute is a string attribute holding that unit's ID.
  #
  # The public surface is Ruleset (decode, evaluate, evaluate_all,
  # unsupported_rules, invalid_conditions, version), Context, Result,
  # ExperimentDetail, Construct, DecodeError, REASONS and ERROR_CODES.
  # Everything else, the hash, date, version and regular expression
  # machinery and the structs a decoded ruleset is made of, is internal
  # and changes without notice.
  module Eval
    private_constant :Murmur3, :Semver, :Instant, :Regex, :Operators, :Values, :Index, :Compiler, :Evaluation,
                     :Document, :UnicodeTables, :Environment, :Attribute, :Segment, :Condition, :Flag, :Variation,
                     :Prerequisite, :Override, :Rule, :Serve, :Weight, :BUCKETS, :SCHEMA_VERSION, :SCOPE,
                     :CONDITION_KINDS, :SERVE_KINDS
  end
end
