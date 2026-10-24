# frozen_string_literal: true

require_relative "generations"
require_relative "event_batch"
require_relative "reporter"
require_relative "uuid"

module Flagnetic
  # Exposures queues exposures and aliases: the explicit
  # exposure call, automatic exposure, the alias call, suppression of
  # repeats bounded in memory, and the flush that the background thread and
  # Client#flush/#close drive with backoff. Offline: nothing is queued,
  # every event goes to the Recorder at the call itself.
  #
  # Thread-safe: evaluated, exposure and add_alias run on whatever thread
  # called a getter or the exposure or alias method; flush_once runs on the
  # background thread and, for an explicit flush or close, whatever thread
  # called it.
  class Exposures
    include Reporter

    QUEUE_CAPACITY = 10_000
    SUPPRESSION_CAPACITY = 10_000
    DETAIL_CAPACITY = 10_000

    # ExposureRecord is what evaluated remembers for a later exposure call:
    # the experiment block, the evaluation reason and the ruleset version
    # the evaluation used.
    ExposureRecord = Struct.new(:experiment, :reason, :ruleset_version, keyword_init: true)

    # The predeclared attributes an exposure carries from the context.
    ATTRIBUTE_PLATFORM = "platform"
    ATTRIBUTE_APP_VERSION = "app_version"
    private_constant :ATTRIBUTE_PLATFORM, :ATTRIBUTE_APP_VERSION

    # wake: a callable poked after enqueuing, so a background thread
    # sleeping on a longer, unrelated deadline notices the new event
    # without waiting for it; a no-op where nothing needs waking (offline,
    # or no worker yet). platform and app_version stand in for a context
    # that names neither.
    def initialize(timing:, log:, transport: nil, record: nil, platform: nil, app_version: nil,
                   capacity: QUEUE_CAPACITY, wake: -> {})
      @transport = transport
      @timing = timing
      @log = log
      @record = record
      @platform = platform
      @app_version = app_version
      @capacity = capacity
      @wake = wake
      @mutex = Mutex.new
      @queue = []
      @suppressed = Generations.new(capacity: SUPPRESSION_CAPACITY)
      @suppressed_since = now
      @details = Generations.new(capacity: DETAIL_CAPACITY)
      @experiment_flags = {}
      @backoff = Backoff.new(base: timing.fetch_base, cap: timing.fetch_cap)
      @not_before = 0
      @flushed_at = now
      @revoked_logged = false
      @send_mutex = Mutex.new
    end

    # offline? reports whether events go to the Recorder rather than to the
    # events endpoint. An offline queue is driven by nothing: every event is
    # recorded at the call itself.
    def offline? = !@record.nil?

    # evaluated remembers the detail of one evaluation for a later exposure
    # call, and records the exposure at once for a flag with
    # automatic_exposure. A flag whose evaluation stops carrying an
    # experiment block is forgotten, so a later exposure call reports
    # nothing rather than the last assignment of an experiment the ruleset
    # no longer serves. The fingerprint of context is computed only when a
    # detail actually needs remembering or forgetting: the great majority
    # of evaluations, of flags that have never carried an experiment,
    # touch nothing but @experiment_flags.
    def evaluated(context, key, detail, automatic)
      unless detail.experiment
        forget(detail_key(key, context), detail.ruleset_version) if known?(key)
        return
      end
      mark_known(key)
      record = ExposureRecord.new(experiment: detail.experiment, reason: detail.reason,
                                  ruleset_version: detail.ruleset_version)
      remember(detail_key(key, context), record)
      emit(context, key, record) if automatic
    end

    # exposure looks up the most recent detail of key for context and
    # enqueues an exposure only when it carries an experiment block;
    # otherwise nothing is sent and exposure_skipped names why.
    def exposure(context, key)
      record, found = recall(detail_key(key, context))
      return skipped(key, cause(key)) unless found

      emit(context, key, record)
    end

    # add_alias enqueues an alias event linking two unit IDs.
    def add_alias(from_unit_type, from_unit_id, to_unit_type, to_unit_id)
      t = Time.now
      enqueue({ "type" => "alias", "event_id" => UUID.v7(time: t), "from_unit_type" => from_unit_type.to_s,
                "from_unit_id" => from_unit_id.to_s, "to_unit_type" => to_unit_type.to_s,
                "to_unit_id" => to_unit_id.to_s, "occurred_at" => EventBatch.format_time(t) })
    end

    # due? reports whether a flush should run now: something is queued, the
    # flush interval has passed since the last flush or a full batch is
    # waiting, and the backoff since the last failure has elapsed.
    def due?(mono = now)
      return false if offline? || mono < @not_before

      size = @mutex.synchronize { @queue.size }
      size.positive? && (mono - @flushed_at >= @timing.exposure_flush || size >= EventBatch::MAX_EVENTS_PER_BATCH)
    end

    # next_deadline is when due? might next turn true, for a worker's wait
    # to schedule around; nil when nothing is queued (offline or not). An
    # enqueue wakes the worker, so a batch filling up is seen at once.
    def next_deadline
      size = @mutex.synchronize { @queue.size }
      return nil if offline? || size.zero?
      return @not_before if size >= EventBatch::MAX_EVENTS_PER_BATCH

      [@not_before, @flushed_at + @timing.exposure_flush].max
    end

    # flush_once sends what is queued, retrying with backoff within budget
    # seconds; whatever is not sent stays queued for a later attempt. Safe
    # to call from any thread; concurrent callers wait for each other only
    # up to budget, so one never blocks behind another past its own.
    def flush_once(budget)
      return true if offline?

      deadline = now + budget
      return true unless wait_for_send_lock(deadline)

      begin
        @flushed_at = now
        send_queue(deadline)
      ensure
        @send_mutex.unlock
      end
    end

    # reset_after_fork empties the queue in a forked child, which starts
    # empty; the parent, which never calls this, keeps its own.
    def reset_after_fork
      @mutex.synchronize { @queue.clear }
      @backoff.reset
      @not_before = 0
    end

    private

    def kind = "exposure_or_alias"

    def known?(key) = @mutex.synchronize { @experiment_flags[key] }
    def mark_known(key) = @mutex.synchronize { @experiment_flags[key] = true }

    # emit builds the exposure and enqueues it, unless the same unit has
    # already been recorded seeing this variation of this experiment within
    # the suppression window.
    def emit(context, key, record)
      exp = record.experiment
      if suppressed?([exp.key, exp.unit_type, exp.unit_id, exp.variation])
        skipped(key, "repeat")
        return
      end
      t = Time.now
      coerced = begin
        Eval::Context.coerce(context)
      rescue StandardError
        Eval::Context::EMPTY
      end
      enqueue({ "type" => "exposure", "event_id" => UUID.v7(time: t), "experiment_key" => exp.key, "flag_key" => key,
                "variation_key" => exp.variation, "unit_type" => exp.unit_type, "unit_id" => exp.unit_id,
                "platform" => predeclared(coerced, ATTRIBUTE_PLATFORM) || @platform,
                "app_version" => predeclared(coerced, ATTRIBUTE_APP_VERSION) || @app_version,
                "evaluation_reason" => record.reason.to_s, "override" => exp.override,
                "ruleset_version" => record.ruleset_version, "occurred_at" => EventBatch.format_time(t) })
    end

    def skipped(key, cause)
      log(:debug, :exposure_skipped, flag: key, cause: cause)
    end

    # cause tells the two mistakes behind an exposure call with nothing to
    # report apart, as far as the SDK can: a flag no evaluation of which
    # has ever carried an experiment block, and a flag that has, but not
    # for this context.
    def cause(key) = known?(key) ? "no_detail" : "no_experiment"

    def suppressed?(key)
      @mutex.synchronize do
        t = now
        if t - @suppressed_since >= @timing.exposure_flush
          @suppressed.expire
          @suppressed_since = t
        end
        _, seen = @suppressed.load(key)
        @suppressed.store(key, true) unless seen
        seen
      end
    end

    def detail_key(flag, context) = [flag, fingerprint(context)]

    def remember(key, record)
      @mutex.synchronize do
        held, ok = @details.load(key)
        next if ok && held.ruleset_version > record.ruleset_version

        @details.store(key, record)
      end
    end

    def forget(key, ruleset_version)
      @mutex.synchronize do
        held, ok = @details.load(key)
        next if ok && held.ruleset_version > ruleset_version

        @details.delete(key)
      end
    end

    def recall(key) = @mutex.synchronize { @details.load(key) }

    # fingerprint is a canonical, in-process encoding of a context, so an
    # exposure call finds the detail of the very context the flag was
    # evaluated for. It never leaves the process and needs no cross-language
    # stability.
    def fingerprint(context)
      c = Eval::Context.coerce(context)
      units = c.units.sort.flatten
      attributes = c.attributes.sort.map { |k, v| [k, attribute_token(v)] }.flatten
      (units + [""] + attributes).join(" ")
    rescue StandardError
      context.object_id.to_s
    end

    def attribute_token(value)
      case value
      when String, Numeric, true, false then value.inspect
      when Array then value.map(&:inspect).join(",")
      when Time then value.utc.iso8601(3)
      else value.to_s
      end
    end

    # predeclared reads one predeclared attribute as a string from an
    # already-coerced context; nil when the context names none or names it
    # as another type, and the client's option then stands in.
    def predeclared(coerced_context, name)
      v = coerced_context.attributes[name]
      v.is_a?(String) ? v : nil
    end

    # enqueue puts one event in the queue, dropping the oldest when the
    # queue is full, and wakes whatever is waiting for a longer deadline.
    # An offline queue hands the event to the Recorder instead.
    def enqueue(event)
      if offline?
        @record.call(event)
        return
      end
      dropped = @mutex.synchronize do
        @queue << event
        excess = @queue.size - @capacity
        excess.positive? ? @queue.shift(excess).size : 0
      end
      @wake.call
      return unless dropped.positive?

      log(:warn, :queue_dropped, kind: kind, cause: "queue_full", events: dropped, capacity: @capacity)
    end

    def take = @mutex.synchronize { @queue.dup.tap { @queue.clear } }

    # put_back returns events the platform did not answer to the front of
    # the queue, so the next flush sends them again with the same event
    # IDs. They are the oldest, so they are the first dropped when the
    # queue is full.
    def put_back(events)
      return if events.empty?

      dropped = @mutex.synchronize do
        @queue = events + @queue
        excess = @queue.size - @capacity
        excess.positive? ? @queue.shift(excess).size : 0
      end
      return unless dropped.positive?

      log(:warn, :queue_dropped, kind: kind, cause: "queue_full", events: dropped, capacity: @capacity)
    end

    def send_queue(deadline)
      events = take
      return true if events.empty?

      batches = EventBatch.pack(events, @transport.identity)
      batches.each_with_index do |group, i|
        result = attempt(group, deadline)
        case result.kind
        when :accepted
          @backoff.reset
          @not_before = 0
          @revoked_logged = false
          report_accepted(group, result)
        when :dropped
          report_dropped(group, result)
        else # :retry or :unauthorized ran out of the flush's budget
          put_back(batches[i..].flatten(1))
          return false
        end
      end
      true
    end

    # schedule_retry backs off on its own schedule rather than honouring
    # Retry-After: the flush budget is seconds, and a header naming
    # minutes must not hold exposures back for minutes (the protocol
    # allows either choice).
    def schedule_retry(group, result)
      wait = @backoff.next_delay(cap: @timing.fetch_cap)
      @not_before = now + wait
      log_ingest_failed(group, result, wait)
      wait
    end

    def report_accepted(group, result)
      return if result.rejected.to_a.empty?

      log(:warn, :queue_dropped, kind: kind, cause: "refused_by_the_platform",
                                 events: group.map { |e| e["event_id"] },
                                 accepted: result.accepted, duplicates: result.duplicates,
                                 rejected: result.rejected.map { |r| r["code"] })
    end
  end
end
