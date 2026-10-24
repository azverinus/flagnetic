# frozen_string_literal: true

require "net/http"

module Flagnetic
  # Stream is one request of the change stream, read on a thread of its own
  # so that the worker can keep its timers while the stream is quiet. What
  # happens is posted to the worker's inbox as [:stream, id, kind, ...]:
  #
  # - :head, status: the response head arrived.
  # - :frame, kind, value: a parsed frame of a 200 response.
  # - :ended, cause, error: the request is over; error is false for the
  #   server closing the stream, true for anything else, a connection that
  #   fails, a malformed stream, or silence for longer than the read
  #   timeout the stream is held with.
  #
  # The read timeout is the silence limit: Net::HTTP raises when no byte,
  # a heartbeat included, arrives within it.
  class Stream
    def initialize(id, transport, held_version, silence:, inbox:)
      @id = id
      @transport = transport
      @request = transport.stream_request(held_version)
      @silence = silence
      @inbox = inbox
      @thread = Thread.new { run }
      @thread.name = "flagnetic-stream"
      @thread.report_on_exception = false
    end

    attr_reader :id

    # close ends the request. Killing the thread runs the ensure of
    # Net::HTTP#start, which closes the socket.
    def close
      @thread.kill
      @thread.join(1)
    end

    private

    def run
      http = @transport.http_for(@transport.stream_uri, @transport.timeout)
      http.start { |h| h.request(@request) { |response| read(h, response) } }
      post(:ended, "closed by the server", false)
    rescue Net::ReadTimeout
      post(:ended, "no frame for #{@silence} s", true)
    rescue StandardError => e
      post(:ended, "#{e.class}: #{e.message}", true)
    end

    def read(http, response)
      status = response.code.to_i
      post(:head, status)
      return unless status == 200

      http.read_timeout = @silence
      parser = SSEParser.new
      response.read_body { |chunk| parser.feed(chunk) { |kind, value| post(:frame, kind, value) } }
    end

    def post(*message) = @inbox << [:stream, @id, *message]
  end
  private_constant :Stream
end
