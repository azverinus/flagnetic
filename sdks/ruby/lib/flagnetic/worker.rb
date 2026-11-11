# frozen_string_literal: true

require "json"

module Flagnetic
  # Worker is a client's background thread. It fetches the ruleset after
  # every (re)connect of the change stream, on every event above the held
  # version and on the poll interval, and retries with backoff; it flushes
  # the exposure and alias queue and the evaluation summary window on
  # their intervals. The stream
  # itself is read by a Stream thread that posts what happens to the
  # worker's inbox, which the worker waits on together with its next
  # deadline. It never raises: a failure is a log event and a later attempt,
  # whatever the failure is, and a thread that dies anyway is logged and
  # started again after a backoff, because it is the only thing keeping the
  # process's ruleset current.
  class Worker
    def initialize(client, transport, timing, stream:)
      @client = client
      @transport = transport
      @timing = timing
      @stream_enabled = stream
      @inbox = Queue.new
      @stop = false
      @stream_ids = 0
      @thread = Thread.new { supervise }
      @thread.name = "flagnetic"
      @thread.report_on_exception = false
    end

    # stop ends the thread within timeout seconds, killing it when it is
    # stuck in a fetch, and closes the stream. Called from the thread itself
    # (a listener closing the client), it only asks the loop to end: the
    # thread cannot wait for itself.
    def stop(timeout)
      @stop = true
      wake
      return if Thread.current == @thread

      # A killed thread still runs its ensure, which closes the stream; it
      # is not waited for, so close returns within timeout.
      @thread.kill unless @thread.join([timeout, 0].max)
      nil
    rescue Exception # rubocop:disable Lint/RescueException
      # join re-raises what the thread died of; that was logged when it
      # happened and is not the host's to handle. Anything else came into
      # the caller while it waited (an Interrupt, a Timeout) and is the
      # host's: a thread that is still alive, or ended normally, did not
      # raise it.
      raise unless @thread.status.nil?

      nil
    end

    # abandon forgets the threads of a parent process after a fork: they do
    # not exist in the child, and the stream's socket is left to the
    # garbage collector there, which closes the child's descriptor without
    # shutting down the TLS session the parent still uses.
    def abandon
      @stop = true
      @stream = nil
    end

    # wake interrupts the background thread's current wait. Safe to call
    # from any thread, including after close.
    def wake
      @inbox << :wake
    end

    private

    def now = Process.clock_gettime(Process::CLOCK_MONOTONIC)

    # supervise runs the loop and starts it again, after a backoff, when it
    # ends by an exception: step already absorbs failures of one turn, so
    # this is for what escapes it (a failure in the loop's own setup, or an
    # exception step re-raised). A backoff that is not reset by a long
    # enough run keeps a crash loop from spinning.
    def supervise
      deaths = Backoff.new(base: @timing.fetch_base, cap: @timing.fetch_cap)
      until @stop
        started = now
        begin
          run
        rescue SystemExit
          return # exit called inside a listener: nothing to restart for
        rescue Exception => e # rubocop:disable Lint/RescueException
          @client.__send__(:log, :error, :worker_died, cause: "#{e.class}: #{e.message}")
          deaths.reset if now - started >= @timing.stream_stable
          sleep deaths.next_delay unless @stop
        end
      end
    end

    def run
      @fetch_backoff = Backoff.new(base: @timing.fetch_base, cap: @timing.fetch_cap)
      @stream_backoff = Backoff.new(base: @timing.stream_base, cap: @timing.stream_cap)
      @refused_backoff = Backoff.new(base: @timing.fetch_base, cap: @timing.unauthorized_cap)
      @next_fetch = now
      @stream_due = @stream_enabled ? now : nil
      @connected = false
      @want = nil
      @event_retry = 0
      step until @stop
    ensure
      @stream&.close
      @stream = nil
    end

    # step opens the stream when due, fetches when due, flushes the
    # exposure and summary queues when either is due, then waits for the
    # stream, a wake-up or the next deadline. The stream is opened before
    # the first fetch, so no change is missed between the two.
    def step
      open_stream if @stream_due && now >= @stream_due
      fetch if !@stop && now >= @next_fetch
      drive(@client.__send__(:exposures), @timing.exposure_flush) unless @stop
      drive(@client.__send__(:summaries), @timing.summary_flush) unless @stop
      wait unless @stop
    rescue SystemExit, NoMemoryError
      raise
    rescue Exception => e # rubocop:disable Lint/RescueException
      # Not only StandardError: a SystemStackError from a deeply nested
      # document or a failure raised by a listener must not end the only
      # thread that follows the ruleset.
      @client.__send__(:log, :error, :worker_failed, cause: "#{e.class}: #{e.message}")
      sleep 1 unless @stop
    end

    # drive flushes queue once when it is due; a queue that is off is nil
    # and this is a no-op. It never lets a flush failure stop the loop. The
    # budget is the interval but at most one request timeout: this thread
    # also fetches the ruleset and reads the stream's messages, so a flush
    # retrying through an events outage for a whole minute would hold back
    # every flag change for that minute. A flush cut short keeps what it
    # did not deliver and is due again after its backoff.
    def drive(queue, interval)
      return unless queue&.due?(now)

      queue.flush_once([interval, @timing.request_timeout].min)
    rescue SystemExit, NoMemoryError
      raise
    rescue Exception => e # rubocop:disable Lint/RescueException
      @client.__send__(:log, :error, :worker_failed, cause: "#{e.class}: #{e.message}")
    end

    def open_stream
      @stream_due = nil
      @stream = Stream.new(@stream_ids += 1, @transport, @client.__send__(:held_version),
                           silence: @timing.silence, inbox: @inbox)
      @head_pending = true
      await_head
    end

    # await_head waits for the response head of the stream just opened, so
    # that the fetch which follows knows whether the stream is up and a
    # connect does not fetch a second time. Stream bounds its own requests
    # with the request timeout, so the wait ends even if no message does.
    def await_head
      deadline = now + @timing.request_timeout + 1
      while @head_pending && !@stop
        message = @inbox.pop(timeout: [deadline - now, 0].max) or break
        handle(message)
      end
      @head_pending = false
    end

    def wait
      deadlines = [@next_fetch]
      deadlines << @stream_due if @stream_due
      deadlines << @client.__send__(:exposures)&.next_deadline
      deadlines << @client.__send__(:summaries)&.next_deadline
      deadlines.compact!
      message = @inbox.pop(timeout: [deadlines.min - now, 0].max)
      handle(message) if message
      handle(@inbox.pop) until @inbox.empty?
    end

    def handle(message)
      return if message == :wake

      _, id, kind, *rest = message
      return unless @stream&.id == id

      case kind
      when :head then head(*rest)
      when :frame then frame(*rest)
      when :ended then drop(rest[0], error: rest[1])
      end
    end

    def head(status)
      @head_pending = false
      case status
      when 200 then connected
      when 401, 403 then refused(status)
      else drop("HTTP #{status}", error: true)
      end
    end

    # refused leaves a stream the key may not open closed, and tries again
    # at the fetch loop's pace while the key is refused: backoff capped at
    # the unauthorized cap, so a restored key gets its stream back.
    def refused(status)
      close_stream
      @client.__send__(:key_revoked, status, "stream")
      @stream_due = now + @refused_backoff.next_delay
    end

    def connected
      @refused_backoff.reset
      @connected = true
      @connected_at = now
      @client.__send__(:stream_connected)
      @next_fetch = now # versions may have been missed while down
    end

    def close_stream
      @head_pending = false
      @stream&.close
      @stream = nil
      @connected = false
    end

    # drop ends the stream and schedules a reconnect: 0 to close_jitter
    # seconds after a server-side close, else the backoff, reset once a
    # connection stayed up stream_stable seconds.
    def drop(cause, error:)
      @stream_backoff.reset if @connected && now - @connected_at >= @timing.stream_stable
      close_stream
      @client.__send__(:log, error ? :warn : :info, :sse_dropped, cause: cause)
      delay = error ? @stream_backoff.next_delay : rand * @timing.close_jitter
      @stream_due = now + delay
      @next_fetch = [@next_fetch, now + @timing.poll_down].min
    end

    def fetch
      response = begin
        @transport.fetch_ruleset(@client.__send__(:held_version))
      rescue StandardError => e
        e
      end
      schedule(@client.__send__(:handle_fetch, response))
    end

    def schedule(outcome)
      case outcome
      when :ok
        @fetch_backoff.reset
        @next_fetch = now + (@connected ? @timing.poll_connected : @timing.poll_down)
        resume_stream
      when :revoked
        @next_fetch = now + @fetch_backoff.next_delay(cap: @timing.unauthorized_cap)
        close_revoked_stream
      else
        @next_fetch = now + @fetch_backoff.next_delay
      end
      retry_event
    end

    # retry_event re-fetches after 1, 2 and 4 s while a fetch answers below
    # the version an event announced: a replica may still be behind.
    def retry_event
      return unless @want

      held = @client.__send__(:held_version)
      if (held && held >= @want) || @event_retry >= @timing.event_retries.size
        @want = nil
        return
      end
      @next_fetch = [@next_fetch, now + @timing.event_retries[@event_retry]].min
      @event_retry += 1
    end

    # A revoked key closes the stream; a later successful fetch, after an
    # operator restores the key, opens it again.
    def close_revoked_stream
      return unless @stream

      close_stream
      @stream_revoked = true
    end

    def resume_stream
      return unless @stream_revoked

      @stream_revoked = false
      @stream_due = now if @stream_enabled
    end

    def frame(kind, value)
      return unless kind == :event && value.type == "ruleset"

      version = begin
        JSON.parse(value.data)["version"]
      rescue JSON::ParserError, TypeError, NoMethodError
        nil
      end
      version = Integer(value.id, exception: false) unless version.is_a?(Integer)
      return unless version

      held = @client.__send__(:held_version)
      return if held && version <= held

      @want = [@want || 0, version].max
      @event_retry = 0
      @next_fetch = now
    end
  end
  private_constant :Worker
end
