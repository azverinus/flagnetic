# frozen_string_literal: true

require "net/http"
require "uri"

module Flagnetic
  # Transport speaks the SDK API under SDK_API_PATH for one server key. Every
  # request, the ruleset stream included, goes through Net::HTTP, so the
  # proxy the process is configured with (the http_proxy, https_proxy and
  # no_proxy environment variables) applies to all of them.
  class Transport
    # Response is the outcome of a ruleset fetch; body is decompressed.
    Response = Struct.new(:status, :body, keyword_init: true)

    # EventsResponse is the outcome of an events POST; EventBatch classifies
    # it. body is decompressed; retry_after is the raw header value.
    EventsResponse = Struct.new(:status, :body, :retry_after, keyword_init: true)

    # BodyTooLarge is a response body that grew past its limit. A fetch
    # raises it like any network error, so it counts as a failed fetch.
    class BodyTooLarge < StandardError; end

    # RULESET_LIMIT bounds a ruleset body after decompression; a ruleset the
    # server accepts is far smaller, so a larger one is a misbehaving peer.
    RULESET_LIMIT = 256 << 20
    # ANSWER_LIMIT bounds the answer to an events POST, a few counters and
    # rejection reasons.
    ANSWER_LIMIT = 1 << 20

    attr_reader :stream_uri, :timeout

    # deprecated, when given, is called with an operation's name (its
    # operationId in api/sdk.yaml) and the raw Deprecation and Sunset header
    # values of every answer that carries a Deprecation header; it decides
    # how often to report. A failure of its own never reaches a request.
    def initialize(sdk_key:, base_url:, platform:, timeout:, deprecated: nil, ruleset_limit: RULESET_LIMIT,
                   answer_limit: ANSWER_LIMIT)
      @deprecated = deprecated
      @ruleset_limit = ruleset_limit
      @answer_limit = answer_limit
      base = URI(base_url.to_s.chomp("/"))
      raise ArgumentError, "base_url must be an http or https URL, got #{base_url.inspect}" unless
        base.is_a?(URI::HTTP) && base.host

      @ruleset_uri = URI("#{base}#{SDK_API_PATH}/ruleset")
      @stream_uri = URI("#{base}#{SDK_API_PATH}/stream")
      @events_uri = URI("#{base}#{SDK_API_PATH}/events")
      @platform = platform
      @sdk_key = sdk_key.to_s
      @user_agent = "flagnetic-ruby/#{VERSION} (#{platform})"
      @timeout = timeout
    end

    # identity is this SDK's entry in an events envelope's sdk member.
    def identity
      { "name" => "flagnetic-ruby", "version" => VERSION, "platform" => @platform }
    end

    # post_events POSTs a gzipped events batch and returns the raw
    # response; EventBatch classifies it. It raises on network errors and
    # timeouts, as fetch_ruleset does. timeout bounds this one call, so a
    # flush bounded by a budget shorter than the client's own
    # request_timeout still returns within it.
    def post_events(gzipped_body, timeout = @timeout)
      request = Net::HTTP::Post.new(@events_uri)
      headers.each { |k, v| request[k] = v }
      request["Content-Type"] = "application/json"
      request["Content-Encoding"] = "gzip"
      request["Flagnetic-Schema-Version"] = SCHEMA_VERSION_MAX.to_s
      request.body = gzipped_body
      status = retry_after = body = nil
      http_for(@events_uri, timeout).start do |h|
        h.request(request) do |response|
          status = response.code.to_i
          retry_after = response["Retry-After"]
          note_deprecation("postEvents", response)
          body = begin
            read_body(response, @answer_limit)
          rescue BodyTooLarge
            # An answer past the limit is cut off, not an error: the batch
            # reached the server, and EventBatch reads an undecodable
            # answer as accepted without counts, so it is not sent twice.
            ""
          end
        end
      end
      EventsResponse.new(status: status, body: body, retry_after: retry_after)
    end

    # fetch_ruleset GETs the ruleset, revalidating the held version with
    # If-None-Match. It raises on network errors and timeouts; Net::HTTP
    # asks for and inflates gzip, and the inflated body is bounded.
    def fetch_ruleset(held_version)
      request = Net::HTTP::Get.new(@ruleset_uri)
      headers.each { |k, v| request[k] = v }
      request["Accept"] = "application/json"
      request["Flagnetic-Schema-Version"] = SCHEMA_VERSION_MAX.to_s
      request["If-None-Match"] = %("#{held_version}") if held_version
      status = body = nil
      http_for(@ruleset_uri, @timeout).start do |h|
        h.request(request) do |response|
          status = response.code.to_i
          note_deprecation("getRuleset", response)
          body = read_body(response, @ruleset_limit)
        end
      end
      Response.new(status: status, body: body)
    end

    # note_deprecation reports a Deprecation header on an answer to
    # operation. Every answer to a deprecated operation carries it, errors
    # included, so it is read from the head before the body.
    def note_deprecation(operation, response)
      header = response["Deprecation"] or return

      @deprecated&.call(operation, header, response["Sunset"])
    rescue StandardError
      nil
    end

    # stream_request is the ruleset stream's request; Last-Event-ID is the
    # held version, so the platform emits only what is newer. The stream is
    # asked for uncompressed: a compressed one would hold frames back until
    # the compressor flushed.
    def stream_request(held_version)
      request = Net::HTTP::Get.new(@stream_uri)
      headers.each { |k, v| request[k] = v }
      request["Accept"] = "text/event-stream"
      request["Cache-Control"] = "no-cache"
      request["Accept-Encoding"] = "identity"
      request["Last-Event-ID"] = held_version.to_s if held_version
      request
    end

    # http_for builds the connection to uri's host. Net::HTTP.new takes the
    # proxy from the environment, honouring no_proxy; retries are off
    # because a failed request is retried by the client's backoff alone.
    def http_for(uri, timeout)
      http = Net::HTTP.new(uri.host, uri.port)
      http.use_ssl = uri.scheme == "https"
      http.open_timeout = http.read_timeout = http.write_timeout = http.ssl_timeout = timeout
      http.max_retries = 0
      http
    end

    # inspect leaves the server key out, so a client printed in a console
    # or attached to an error report does not disclose it.
    def inspect = "#<#{self.class} #{@ruleset_uri.host}:#{@ruleset_uri.port}>"

    private

    # read_body gathers the body in the pieces Net::HTTP yields, which are
    # inflated when the answer is gzip, and raises as soon as their total
    # passes limit: a Content-Length or a compressed size says nothing of
    # what a peer will make the process hold.
    def read_body(response, limit)
      body = +"".b
      response.read_body do |piece|
        body << piece
        raise BodyTooLarge, "the response body exceeds #{limit} bytes" if body.bytesize > limit
      end
      body.force_encoding(Encoding::UTF_8)
    end

    def headers
      { "Authorization" => "Bearer #{@sdk_key}", "User-Agent" => @user_agent }
    end
  end
  private_constant :Transport
end
