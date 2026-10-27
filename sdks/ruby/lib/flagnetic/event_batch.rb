# frozen_string_literal: true

require "json"
require "time"
require "zlib"
require "stringio"

module Flagnetic
  # EventBatch builds and sends the events endpoint's envelope for exposures,
  # aliases and evaluation summaries alike: the gzip body, batching under
  # the endpoint's limits, and what the platform's answer means for the
  # caller. Shared by Exposures and Summaries; it holds no queue and no
  # timers of its own.
  module EventBatch
    # Limits of POST /sdk/v1/events; they bound the decompressed
    # envelope, so pack packs against them before the caller gzips.
    MAX_EVENTS_PER_BATCH = 500
    MAX_BATCH_BYTES = 1 << 20
    MAX_RESULT_BYTES = 1 << 20

    # Outcome is what the platform did with one batch, and what the caller
    # does next: :accepted (202; done, rejected events included, a
    # rejection is final), :retry (429, 5xx or a network error: the same
    # events may be sent again), :unauthorized (401/403: retriable too, but
    # at the unauthorized cap, since an operator restoring the key is what
    # ends it) or :dropped (any other 4xx: sending it again would fail the
    # same way).
    Outcome = Struct.new(:kind, :status, :retry_after, :accepted, :duplicates, :dropped_rows, :rejected, :error,
                         keyword_init: true)

    module_function

    # pack splits events (an Array of Hash, JSON-ready) into batches the
    # endpoint accepts: at most 500 events, and under 1 MiB decoded
    # together with the envelope. An event too large on its own still
    # becomes a batch of one, which the platform answers 413 and the
    # caller drops.
    def pack(events, identity)
      budget = MAX_BATCH_BYTES - overhead(identity)
      batches = []
      current = []
      size = 0
      events.each do |e|
        cost = JSON.generate(e).bytesize + 1 # one comma per event after the first
        if current.any? && (current.size == MAX_EVENTS_PER_BATCH || size + cost > budget)
          batches << current
          current = []
          size = 0
        end
        current << e
        size += cost
      end
      batches << current if current.any?
      batches
    end

    # send_batch posts one batch through transport, bounding the call by
    # timeout seconds, and classifies the answer. It never raises.
    def send_batch(transport, events, timeout)
      classify(transport.post_events(encode(transport.identity, events), timeout))
    rescue StandardError => e
      Outcome.new(kind: :retry, error: "#{e.class}: #{e.message}")
    end

    def envelope(identity, events)
      { "schema_version" => SCHEMA_VERSION_MAX, "sdk" => identity, "sent_at" => format_time(Time.now),
        "events" => events }
    end

    def encode(identity, events)
      doc = JSON.generate(envelope(identity, events))
      io = StringIO.new
      gz = Zlib::GzipWriter.new(io)
      gz.write(doc)
      gz.close
      io.string
    end

    # overhead is how many bytes an empty envelope costs, so pack can stay
    # under the endpoint's body limit; it is the decoded size, since the
    # limit applies to the decompressed body.
    def overhead(identity)
      JSON.generate(envelope(identity, [])).bytesize
    end

    def format_time(time) = time.utc.iso8601(3)

    def classify(response)
      case response.status
      when 202 then accepted(response)
      when 429 then Outcome.new(kind: :retry, status: 429, retry_after: retry_after(response.retry_after))
      when 401, 403 then Outcome.new(kind: :unauthorized, status: response.status)
      when 500..599 then Outcome.new(kind: :retry, status: response.status)
      else Outcome.new(kind: :dropped, status: response.status)
      end
    end

    # accepted decodes the 202 answer. A body that does not decode leaves
    # the counts unset: the batch was accepted either way, so nothing is
    # retried.
    def accepted(response)
      json = JSON.parse(response.body.to_s.byteslice(0, MAX_RESULT_BYTES))
      Outcome.new(kind: :accepted, status: 202, accepted: json["accepted"], duplicates: json["duplicates"],
                  dropped_rows: json["dropped_rows"], rejected: json["rejected"] || [])
    rescue JSON::ParserError, TypeError
      Outcome.new(kind: :accepted, status: 202, rejected: [])
    end

    # retry_after reads a Retry-After header in its delta-seconds form; an
    # HTTP date or anything else means the caller backs off on its own.
    def retry_after(header)
      return nil unless header

      seconds = Integer(header, exception: false)
      seconds && !seconds.negative? ? seconds : nil
    end
  end
  private_constant :EventBatch
end
