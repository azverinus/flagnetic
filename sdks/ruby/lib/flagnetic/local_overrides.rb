# frozen_string_literal: true

require "json"

module Flagnetic
  # LocalOverrides substitutes flag values inside this process, on a
  # developer's machine or in a non-production build. A substituted value
  # never reaches the server. An overridden flag reads reason
  # local_override, with no experiment block and the stage unchanged.
  #
  # Local overrides exist only on a client constructed with
  # allow_overrides: true, and are refused when the held ruleset belongs to
  # a protected environment: set raises LocalOverrideRefused and logs
  # override_refused, and a protected ruleset arriving later drops every
  # override. Setting or removing one fires on_change.
  #
  #   client.local_overrides.set("new_checkout", true)
  class LocalOverrides
    def initialize(client)
      @client = client
    end

    # set substitutes value for the flag key. The value must be of the
    # flag's type: true or false, a String, a Numeric, or for a JSON flag
    # anything JSON can encode. A key the ruleset lacks takes the type of
    # the value. Raises LocalOverridesNotAllowed, LocalOverrideRefused, or
    # ArgumentError for a value of the wrong type.
    def set(key, value)
      @client.__send__(:set_local_override, key.to_s, value)
      nil
    end

    # remove drops the local override of key, firing on_change when there
    # was one.
    def remove(key)
      @client.__send__(:remove_local_overrides, [key.to_s])
      nil
    end

    # clear drops every local override, firing on_change with their keys.
    def clear
      @client.__send__(:remove_local_overrides, nil)
      nil
    end

    # list returns the local overrides by flag key.
    def list = @client.__send__(:local_override_values)

    # normalize checks value against a flag type and returns the frozen
    # value to serve.
    def self.normalize(type, value)
      case type
      when "boolean" then return value if [true, false].include?(value)
      when "string" then return value.dup.freeze if value.is_a?(String)
      when "number" then return value if value.is_a?(Numeric) && value.real? && value.to_f.finite?
      when "json" then return JSON.parse(JSON.generate(value), freeze: true)
      else raise ArgumentError, "flag type #{type.inspect} is unknown"
      end
      raise ArgumentError, "a #{value.class} is not a value of a #{type} flag"
    rescue JSON::GeneratorError, JSON::ParserError => e
      raise ArgumentError, "a #{value.class} is not a JSON value: #{e.message}"
    end
  end
end
