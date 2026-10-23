# frozen_string_literal: true

module Flagnetic
  module Eval
    # Semver is a parsed semantic version. Build metadata is dropped because
    # it never affects precedence.
    class Semver
      include Comparable

      CORE = /\A[0-9]+(?:\.[0-9]+){0,2}\z/
      IDENTIFIERS = /\A[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*\z/
      NUMERIC = /\A[0-9]+\z/
      private_constant :CORE, :IDENTIFIERS, :NUMERIC

      # parse reads an optional lower-case "v", one to three dot-separated
      # non-negative integers (missing components are 0, so "1.41" equals
      # "1.41.0"), an optional "-" pre-release and an optional "+" build.
      # Numeric components are unbounded and leading zeros are ignored.
      # Pre-release and build identifiers are non-empty runs of
      # [0-9A-Za-z-]. It returns nil for anything else.
      def self.parse(str)
        return nil unless str.is_a?(String) && str.valid_encoding?

        s = str.delete_prefix("v")
        s, build = s.split("+", 2)
        return nil if build && !build.match?(IDENTIFIERS)

        s, prerelease = s.to_s.split("-", 2)
        return nil if prerelease && !prerelease.match?(IDENTIFIERS)
        return nil unless s.to_s.match?(CORE)

        core = s.split(".").map(&:to_i)
        new(core.fill(0, core.size...3), prerelease ? prerelease.split(".") : [])
      end

      attr_reader :core, :prerelease

      def initialize(core, prerelease)
        @core = core.freeze
        @prerelease = prerelease.freeze
        freeze
      end

      # <=> orders versions by SemVer 2.0.0 section 11 precedence.
      def <=>(other)
        return nil unless other.is_a?(Semver)

        c = core <=> other.core
        return c unless c.zero?
        return 0 if prerelease.empty? && other.prerelease.empty?
        return 1 if prerelease.empty?
        return -1 if other.prerelease.empty?

        prerelease.zip(other.prerelease).each do |a, b|
          break if b.nil?

          c = compare_identifiers(a, b)
          return c unless c.zero?
        end
        prerelease.size <=> other.prerelease.size
      end

      private

      # compare_identifiers orders pre-release identifiers: numeric ones
      # numerically and below alphanumeric ones, alphanumeric ones in ASCII
      # order.
      def compare_identifiers(left, right)
        ln = left.match?(NUMERIC)
        rn = right.match?(NUMERIC)
        return left.to_i <=> right.to_i if ln && rn
        return -1 if ln
        return 1 if rn

        left.b <=> right.b
      end
    end
  end
end
