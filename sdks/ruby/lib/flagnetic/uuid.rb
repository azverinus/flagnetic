# frozen_string_literal: true

require "securerandom"

module Flagnetic
  # UUID generates the event IDs of the events endpoint.
  module UUID
    module_function

    # v7 returns a lowercase UUIDv7 whose first 48 bits are milliseconds
    # since the epoch, so the platform reads an event's time from the ID
    # itself and drops a retry as a duplicate.
    def v7(time: Time.now)
      ms = [(time.to_r * 1000).to_i, 0].max
      timestamp = [ms].pack("Q>").byteslice(2, 6) # the top 48 bits, big-endian
      entropy = SecureRandom.random_bytes(10)
      version_and_rand_a = ((entropy.getbyte(0) & 0x0f) | 0x70).chr
      variant_and_rand_b = ((entropy.getbyte(1) & 0x3f) | 0x80).chr
      bytes = timestamp + version_and_rand_a + entropy.byteslice(2, 1) + variant_and_rand_b + entropy.byteslice(3, 7)
      hex = bytes.unpack1("H*")
      "#{hex[0, 8]}-#{hex[8, 4]}-#{hex[12, 4]}-#{hex[16, 4]}-#{hex[20, 12]}"
    end
  end
  private_constant :UUID
end
