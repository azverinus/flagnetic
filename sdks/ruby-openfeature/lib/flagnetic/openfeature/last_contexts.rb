# frozen_string_literal: true

module Flagnetic
  module OpenFeature
    # LastContexts remembers the context the provider last evaluated an
    # experiment flag with, per flag and units. OpenFeature runs no hooks for
    # track, so the context track receives lacks what a before hook added to
    # the evaluation, and the client would find no served detail under it:
    # the remembered context is the one the detail is filed under.
    class LastContexts
      # CAPACITY bounds the evaluations remembered; the oldest goes out first.
      CAPACITY = 10_000

      def initialize(capacity: CAPACITY)
        @capacity = capacity
        @mutex = Mutex.new
        @by_key = {}
      end

      def remember(flag, context)
        key = key_of(flag, context)
        @mutex.synchronize do
          @by_key.delete(key)
          @by_key[key] = context
          @by_key.shift while @by_key.size > @capacity
        end
      end

      # forget drops what was remembered, so a flag that no longer carries an
      # experiment block falls back to the tracked context.
      def forget(flag, context)
        key = key_of(flag, context)
        @mutex.synchronize { @by_key.delete(key) }
      end

      # recall returns the context the flag was last evaluated with for the
      # units of fallback, or fallback itself.
      def recall(flag, fallback)
        key = key_of(flag, fallback)
        @mutex.synchronize { @by_key.fetch(key, fallback) }
      end

      def size = @mutex.synchronize { @by_key.size }

      private

      def key_of(flag, context) = [flag, context[:units].sort]
    end
    private_constant :LastContexts
  end
end
