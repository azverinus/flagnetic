# frozen_string_literal: true

require_relative "event_batch"

module Flagnetic
  # Reporter is the send-with-backoff machinery shared by Exposures and
  # Summaries: both post batches through EventBatch under the same retry
  # policy (429, 5xx and network errors retried with backoff bounded by
  # the flush's own budget; 401/403 backed off to the unauthorized cap
  # and logged once) and the same bounded wait for another flush already
  # in progress. schedule_retry is the one place they differ (the protocol
  # allows honouring Retry-After or backing off on its own schedule); each
  # includer defines its own. The includer supplies @transport, @timing,
  # @log, @backoff, @not_before, @revoked_logged, @send_mutex and a
  # private #kind.
  module Reporter
    # MIN_ATTEMPT_TIME is the least budget left an attempt needs to be
    # worth making at all, so a very short flush_interval (tests) still
    # gets one real attempt instead of giving up before trying.
    MIN_ATTEMPT_TIME = 0.01

    private

    def now = Process.clock_gettime(Process::CLOCK_MONOTONIC)

    # wait_for_send_lock waits for @send_mutex, up to deadline, so a flush
    # bounded by its own budget never blocks on another flush already in
    # progress for longer than that budget. Returns whether it was
    # acquired; the caller must unlock it.
    def wait_for_send_lock(deadline)
      until @send_mutex.try_lock
        return false if now >= deadline

        sleep([deadline - now, 0.01].min)
      end
      true
    end

    # attempt sends group, retrying it whole (same events, same event IDs)
    # on 429, 5xx and network errors, until the platform answers it, the
    # deadline is up or the key is refused. Each call is itself bounded by
    # whatever is left of the deadline, so a hung platform never holds a
    # flush past its budget.
    def attempt(group, deadline)
      loop do
        remaining = deadline - now
        if remaining <= MIN_ATTEMPT_TIME
          result = EventBatch::Outcome.new(kind: :retry, error: "flush budget exhausted")
          # schedule_retry's own doing: without it @not_before never
          # advances, and the next due? is immediately true again.
          schedule_retry(group, result)
          return result
        end

        result = EventBatch.send_batch(@transport, group, [remaining, @timing.request_timeout].min)
        case result.kind
        when :retry then wait = schedule_retry(group, result)
        when :unauthorized then (schedule_unauthorized(result)
                                 return result)
        else return result
        end
        return result if now + wait > deadline

        sleep(wait)
      end
    end

    def schedule_unauthorized(result)
      wait = @backoff.next_delay(cap: @timing.unauthorized_cap)
      @not_before = now + wait
      return if @revoked_logged

      @revoked_logged = true
      log(:error, :key_revoked, status: result.status, source: "events")
    end

    def log_ingest_failed(group, result, wait)
      log(:warn, :ingest_failed, kind: kind, events: group.size, retry_in: wait.round(3), status: result.status,
                                 error: result.error)
    end

    def report_dropped(group, result)
      log(:warn, :queue_dropped, kind: kind, cause: "client_error",
                                 events: group.map { |e| e["event_id"] }, status: result.status, error: result.error)
    end

    def log(level, event, **fields)
      @log.call(level, event, fields.compact)
    rescue StandardError
      nil
    end
  end
  private_constant :Reporter
end
