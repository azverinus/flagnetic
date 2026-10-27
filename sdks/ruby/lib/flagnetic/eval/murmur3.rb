# frozen_string_literal: true

module Flagnetic
  # Eval is the evaluator; see flagnetic/eval.rb.
  module Eval
    # BUCKETS is the number of buckets a unit is hashed into. A percentage in
    # basis points (0 to 10000) is compared against a bucket directly.
    BUCKETS = 10_000

    # Murmur3 is MurmurHash3 x86 32-bit with seed 0 over the bytes of a
    # string, read as an unsigned integer. Hashing UTF-16 or reading the
    # result as signed gives different buckets and fails the murmur3 vectors.
    module Murmur3
      C1 = 0xcc9e2d51
      C2 = 0x1b873593
      MASK = 0xffffffff

      module_function

      # bucket computes the bucket of a unit for a salt:
      #
      #   murmur3_32(utf8("{salt}.{unit_id}"), seed 0) mod 10000
      #
      # on the unsigned hash, giving 0 to 9999. A unit is inside
      # percentage p when its bucket is below p. It equals ClickHouse
      # murmurHash3_32(concat(salt, '.', unit_id)) % 10000.
      def bucket(salt, unit_id)
        hash32("#{salt.b}.#{unit_id.b}") % BUCKETS
      end

      def hash32(data)
        bytes = data.b
        h = 0
        blocks = bytes.bytesize / 4
        bytes.unpack("V#{blocks}").each do |k|
          h ^= mix(k)
          h = rotl(h, 13)
          h = ((h * 5) + 0xe6546b64) & MASK
        end
        tail = bytes.byteslice(blocks * 4, 3).bytes
        unless tail.empty?
          k = tail.each_with_index.sum { |b, i| b << (8 * i) }
          h ^= mix(k)
        end
        fmix(h ^ (bytes.bytesize & MASK))
      end

      def mix(key)
        k = (key * C1) & MASK
        k = rotl(k, 15)
        (k * C2) & MASK
      end

      def fmix(hash)
        h = hash
        h ^= h >> 16
        h = (h * 0x85ebca6b) & MASK
        h ^= h >> 13
        h = (h * 0xc2b2ae35) & MASK
        h ^ (h >> 16)
      end

      def rotl(value, bits)
        ((value << bits) | (value >> (32 - bits))) & MASK
      end
      private_class_method :mix, :fmix, :rotl
    end
    private_constant :Murmur3
  end
end
