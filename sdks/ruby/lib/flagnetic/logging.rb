# frozen_string_literal: true

module Flagnetic
  # EVENTS are the stable names of log events: alert on them. The fields
  # each carries are listed in the README.
  EVENTS = %i[
    ruleset_loaded fetch_failed sse_connected sse_dropped stage_changed key_revoked schema_unsupported
    rule_unsupported override_set override_refused local_overrides_allowed type_mismatch evaluation_failed
    listener_failed store_failed worker_failed
  ].freeze

  # LEVELS are the log levels, lowest first.
  LEVELS = %i[debug info warn error].freeze

  # Logging adapts the logger option: a callable receiving
  # (level, event, fields), or a standard library ::Logger (anything
  # responding to add), which receives every level and applies its own.
  module Logging
    module_function

    # SEVERITIES are the standard library Logger's severities by level.
    SEVERITIES = { debug: 0, info: 1, warn: 2, error: 3 }.freeze

    # standard is the default logger: warn and above to $stderr, through
    # the standard library's Logger when the application has loaded it (a
    # Rails application has), else as the same lines through Kernel#warn.
    # The gem never requires logger itself: from Ruby 3.5 it is a bundled
    # gem, which would be a runtime dependency.
    def standard(level: :warn)
      logger = defined?(::Logger) ? ::Logger.new($stderr, progname: "flagnetic") : nil
      min = LEVELS.index(level)
      lambda do |lvl, event, fields|
        next if LEVELS.index(lvl) < min

        line = format_line(event, fields)
        logger ? logger.add(SEVERITIES[lvl], line) : Kernel.warn("flagnetic #{lvl.upcase}: #{line}")
      end
    end

    # adapt turns the logger option into a callable.
    def adapt(logger)
      return standard if logger.nil?
      return logger if logger.respond_to?(:call)

      if logger.respond_to?(:add)
        return lambda { |lvl, event, fields|
          logger.add(SEVERITIES[lvl], format_line(event, fields))
        }
      end

      raise ArgumentError, "logger must respond to call(level, event, fields) or be a Logger, got #{logger.class}"
    end

    def format_line(event, fields)
      ([event.to_s] + fields.map { |k, v| "#{k}=#{v.is_a?(String) ? v.inspect : v}" }).join(" ")
    end
  end
end
