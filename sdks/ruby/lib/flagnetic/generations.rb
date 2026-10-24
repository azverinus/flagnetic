# frozen_string_literal: true

module Flagnetic
  # Generations is a bounded map whose entries survive one rotation: a
  # lookup sees the current generation and the one before it, and a
  # rotation forgets only the older one. It bounds memory without a timer
  # and without the bookkeeping of an eviction order, which is what a
  # suppression set the platform does not rely on needs.
  #
  # It holds no clock and no window: when a generation has lived long
  # enough is the caller's to decide, by calling expire. A full generation
  # rotates on its own, because the bound is the whole point.
  #
  # Not safe for concurrent use; the caller holds a mutex over it.
  class Generations
    def initialize(capacity:)
      @capacity = capacity
      @current = {}
      @previous = {}
    end

    # load returns [value, true] from either generation, or [nil, false].
    def load(key)
      return [@current[key], true] if @current.key?(key)
      return [@previous[key], true] if @previous.key?(key)

      [nil, false]
    end

    # store writes key into the current generation, expiring the older one
    # first when the current one is full.
    def store(key, value)
      expire if !@current.key?(key) && @current.size >= @capacity
      @current[key] = value
    end

    # delete forgets key in both generations.
    def delete(key)
      @current.delete(key)
      @previous.delete(key)
    end

    # expire forgets the older generation and makes the current one older.
    def expire
      @previous = @current
      @current = {}
    end
  end
end
