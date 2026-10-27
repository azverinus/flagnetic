# frozen_string_literal: true

require "time"
require_relative "event_batch"
require_relative "reporter"
require_relative "uuid"

module Flagnetic
  # Summaries counts every evaluation and reports each interval's window as
  # one or more evaluation summary events: counts per (flag, variation,
  # reason), the ruleset version at the interval's end, no unit IDs. A
  # window whose report failed merges into the next one rather than being
  # retried under its own event ID; one older than an hour is dropped
  # instead. Offline: the window rotates only on flush and close, and goes
  # to the Recorder instead of the events endpoint.
  class Summaries
    include Reporter

    MAX_SUMMARY_ROWS = 1000
    # MAX_WINDOW_AGE is how old a window whose report failed may be before
    # it is dropped rather than merged forward: an hour, so a
    # long outage does not eventually deliver counts nobody can place.
    MAX_WINDOW_AGE = 3600

    # LogicalEvent is one evaluation summary before it is wire-encoded:
    # counts as [flag, variation, reason, count] tuples, reason a symbol.
    LogicalEvent = Struct.new(:event_id, :interval_start, :interval_end, :ruleset_version, :counts, keyword_init: true)

    # app_version, when given, goes with every summary.
    def initialize(timing:, log:, ruleset_version:, transport: nil, record: nil, app_version: nil)
      @transport = transport
      @app_version = app_version
      @timing = timing
      @log = log
      @record = record
      @ruleset_version = ruleset_version
      @mutex = Mutex.new
      @wall_start = Time.now
      @window_due_since = now
      @current = Hash.new(0)
      @kept = nil
      @backoff = Backoff.new(base: timing.fetch_base, cap: timing.fetch_cap)
      @not_before = 0
      @revoked_logged = false
      @send_mutex = Mutex.new
    end

    # offline? reports whether the window goes to the Recorder rather than
    # to the events endpoint.
    def offline? = !@record.nil?

    # count records one served value: the flag, the variation and the
    # evaluation reason, never the value and never the context.
    def count(flag, detail)
      key = [flag, detail.variation, detail.reason]
      @mutex.synchronize { @current[key] += 1 }
    end

    # due? reports whether the reporting interval has elapsed and the
    # backoff since the last failure, if any, has too.
    def due?(mono = now)
      return false if offline?

      mono >= @not_before && mono - @window_due_since >= @timing.summary_flush
    end

    # next_deadline is when due? might next turn true, for a worker's wait
    # to schedule around; nil offline, where nothing drives it.
    def next_deadline
      return nil if offline?

      [@not_before, @window_due_since + @timing.summary_flush].max
    end

    # flush_once closes the current window and delivers it (or, offline,
    # hands it to the Recorder), retrying with backoff within budget
    # seconds. Safe to call from any thread; concurrent callers wait for
    # each other only up to budget, so one never blocks behind another
    # past its own.
    def flush_once(budget)
      deadline = now + budget
      return true unless wait_for_send_lock(deadline)

      begin
        w = take_window
        w ? send_window(w, deadline) : true
      ensure
        @send_mutex.unlock
      end
    end

    # reset_after_fork empties the window in a forked child, which starts
    # empty; the parent, which never calls this, keeps its own.
    def reset_after_fork
      @mutex.synchronize do
        @current = Hash.new(0)
        @kept = nil
        @wall_start = Time.now
        @window_due_since = now
      end
      @backoff.reset
      @not_before = 0
    end

    private

    def kind = "evaluation_summary"

    # take_window closes the current window, merging in one a failed
    # report kept unless it began over an hour ago, and returns nil when
    # nothing was evaluated.
    def take_window
      wall_end = Time.now
      win, kept = @mutex.synchronize do
        window = { start: @wall_start, end: wall_end, counts: @current }
        held = @kept
        @wall_start = wall_end
        @window_due_since = now
        @current = Hash.new(0)
        @kept = nil
        [window, held]
      end
      merge_kept(win, kept, wall_end) if kept
      win[:counts].empty? ? nil : win
    end

    def merge_kept(win, kept, wall_end)
      age = wall_end - kept[:start]
      if age > MAX_WINDOW_AGE
        log(:warn, :queue_dropped, kind: kind, rows: kept[:counts].size, cause: "window_older_than_one_hour",
                                   age: age.round(3))
        return
      end
      win[:start] = kept[:start] if kept[:start] < win[:start]
      kept[:counts].each { |k, n| win[:counts][k] = (win[:counts][k] || 0) + n }
    end

    # keep puts a window back, to merge into the next one taken.
    def keep(window)
      @mutex.synchronize do
        if @kept.nil?
          @kept = window
          next
        end
        @kept[:start] = window[:start] if window[:start] < @kept[:start]
        window[:counts].each { |k, n| @kept[:counts][k] = (@kept[:counts][k] || 0) + n }
      end
    end

    def send_window(win, deadline)
      events = build_events(win)
      if offline?
        events.each { |e| @record.call(public_summary(e)) }
        return true
      end

      wire_events = events.map { |e| wire(e) }
      batches = EventBatch.pack(wire_events, @transport.identity)
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
        else
          keep(window_of(batches[i..].flatten(1)))
          return false
        end
      end
      true
    end

    # build_events turns a window into logical summary events: the rows
    # ordered, at most 1000 to an event, each with its own event ID and all
    # naming the window's interval and the ruleset version held at its end.
    def build_events(win)
      rows = win[:counts].map { |(flag, variation, reason), n| { flag:, variation:, reason:, count: n } }
      rows.sort_by! { |r| [r[:flag], r[:variation].to_s, r[:reason].to_s] }
      # Before any ruleset was held the version is 0, which no ruleset has.
      version = @ruleset_version.call || 0
      events = []
      events << LogicalEvent.new(event_id: UUID.v7, interval_start: win[:start], interval_end: win[:end],
                                 ruleset_version: version, counts: rows.shift(MAX_SUMMARY_ROWS)) until rows.empty?
      events
    end

    def wire(event)
      doc = { "type" => "evaluation_summary", "event_id" => event.event_id,
              "interval_start" => EventBatch.format_time(event.interval_start),
              "interval_end" => EventBatch.format_time(event.interval_end),
              "ruleset_version" => event.ruleset_version,
              "counts" => event.counts.map do |r|
                { "flag" => r[:flag], "variation" => r[:variation], "reason" => r[:reason].to_s, "count" => r[:count] }
              end }
      doc["app_version"] = @app_version if @app_version
      doc
    end

    def public_summary(event)
      Recorder::EvaluationSummary.new(
        event_id: event.event_id, interval_start: event.interval_start, interval_end: event.interval_end,
        ruleset_version: event.ruleset_version, app_version: @app_version,
        counts: event.counts.map do |r|
          Recorder::SummaryCount.new(flag: r[:flag], variation: r[:variation],
                                     reason: r[:reason], count: r[:count])
        end
      )
    end

    # window_of rebuilds a window from the summary events that were not
    # delivered, so their rows merge into the next one.
    def window_of(wire_events)
      counts = Hash.new(0)
      start_t = nil
      end_t = nil
      wire_events.each do |e|
        s = Time.parse(e["interval_start"])
        en = Time.parse(e["interval_end"])
        start_t = s if start_t.nil? || s < start_t
        end_t = en if end_t.nil? || en > end_t
        e["counts"].each { |r| counts[[r["flag"], r["variation"], r["reason"].to_sym]] += r["count"] }
      end
      { start: start_t || Time.now, end: end_t || Time.now, counts: counts }
    end

    # schedule_retry honours Retry-After, capped at the ordinary backoff
    # ceiling: a header naming minutes must not silence reporting for
    # minutes (or, from a misbehaving proxy, far longer), so it bounds
    # rather than replaces the backoff (the protocol allows either
    # choice; Exposures ignores the header entirely instead).
    def schedule_retry(group, result)
      wait = if result.retry_after
               [result.retry_after,
                @timing.fetch_cap].min
             else
               @backoff.next_delay(cap: @timing.fetch_cap)
             end
      @not_before = now + wait
      log_ingest_failed(group, result, wait)
      wait
    end

    def report_accepted(group, result)
      return if result.dropped_rows.to_i.zero? && result.rejected.to_a.empty?

      log(:warn, :queue_dropped, kind: kind, cause: "refused_by_the_platform",
                                 events: group.map { |e| e["event_id"] }, accepted: result.accepted,
                                 duplicates: result.duplicates, dropped_rows: result.dropped_rows,
                                 rejected: result.rejected.map { |r| r["code"] })
    end
  end
  private_constant :Summaries
end
