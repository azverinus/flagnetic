# frozen_string_literal: true

require "json"

module Flagnetic
  # Worker is a client's background thread. It fetches the ruleset after
  # every (re)connect of the change stream, on every event above the held
  # version and on the poll interval, and retries with backoff. The stream
  # itself is read by a Stream thread that posts what happens to the
  # worker's inbox, which the worker waits on together with its next
  # deadline. It never raises: a failure is a log event and a later attempt.
  class Worker
    def initialize(client, transport, timing, stream:)
      @client = client
      @transport = transport
      @timing = timing
      @stream_enabled = stream
      @inbox = Queue.new
      @stop = false
      @stream_ids = 0
      @thread = Thread.new { run }
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
    end

    # step opens the stream when due, fetches when due, then waits for the
    # stream, a wake-up or the next deadline. The stream is opened before
    # the first fetch, so no change is missed between the two.
    def step
      open_stream if @stream_due && now >= @stream_due
      fetch if !@stop && now >= @next_fetch
      wait unless @stop
    rescue StandardError => e
      @client.__send__(:log, :error, :worker_failed, cause: "#{e.class}: #{e.message}")
      sleep 1 unless @stop
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
