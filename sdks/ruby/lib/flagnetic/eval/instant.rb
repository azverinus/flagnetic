# frozen_string_literal: true

module Flagnetic
  module Eval
    # Instant reads dates as integer nanoseconds since the Unix epoch, the
    # way the reference evaluator's time.Parse(time.RFC3339Nano) and
    # time.UnixMilli read them.
    module Instant
      RFC3339 = /\A(\d{4})-(\d{2})-(\d{2})T(\d{1,2}):(\d{2}):(\d{2})(?:[.,](\d+))?(?:(Z)|([+-])(\d{2}):(\d{2}))\z/
      MAX_EPOCH_MILLIS = 2**53
      private_constant :RFC3339, :MAX_EPOCH_MILLIS

      module_function

      # from_context reads a context value: an RFC 3339 string with an
      # offset, an integral number of Unix epoch milliseconds or a Time.
      def from_context(value)
        case value
        when String then parse_rfc3339(value)
        when Time then (value.to_r * 1_000_000_000).floor
        when true, false, nil then nil
        else
          ms = Eval.number(value)
          return nil unless ms && ms == ms.truncate && ms.abs <= MAX_EPOCH_MILLIS

          ms.to_i * 1_000_000
        end
      end

      # parse_rfc3339 accepts what Go's time.Parse with RFC3339Nano accepts:
      # an upper-case T and Z, a one- or two-digit hour, a fraction after .
      # or , truncated to nanoseconds, and an offset of at most 24:60.
      def parse_rfc3339(str)
        m = str.valid_encoding? && RFC3339.match(str)
        return nil unless m

        year, month, day, hour, min, sec = m.captures[0, 6].map(&:to_i)
        return nil unless valid_date?(year, month, day) && hour < 24 && min < 60 && sec < 60

        offset = offset_seconds(m)
        return nil unless offset

        seconds = Time.utc(year, month, day, hour, min, sec).to_i - offset
        (seconds * 1_000_000_000) + m[7].to_s[0, 9].ljust(9, "0").to_i
      end

      def offset_seconds(match)
        return 0 if match[8]

        hours = match[10].to_i
        minutes = match[11].to_i
        return nil if hours > 24 || minutes > 60

        seconds = (hours * 3600) + (minutes * 60)
        match[9] == "-" ? -seconds : seconds
      end

      def valid_date?(year, month, day)
        return false unless month.between?(1, 12) && day >= 1

        days = [31, leap?(year) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][month - 1]
        day <= days
      end

      def leap?(year)
        (year % 4).zero? && (!(year % 100).zero? || (year % 400).zero?)
      end
    end
  end
end
