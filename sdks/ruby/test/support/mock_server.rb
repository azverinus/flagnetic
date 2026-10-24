# frozen_string_literal: true

require "json"
require "socket"
require "zlib"
require "stringio"

# MockServer is an in-process SDK API: one thread per connection, one
# request per connection. By default it behaves like the platform: the
# ruleset endpoint serves the current document with ETag and If-None-Match,
# the change stream writes `retry`, the current version when Last-Event-ID
# is below it, and every later version, and the events endpoint answers
# 202 accepting every event it was sent. Tests replace any endpoint with a
# script, publish new versions, and drop or close open streams.
class MockServer
  Request = Struct.new(:verb, :path, :headers, :at, :body, keyword_init: true) do
    def [](name) = headers[name.downcase]

    # decoded_body ungzips body when Content-Encoding says so, as every
    # POST /sdk/v1/events body is.
    def decoded_body
      return body unless self["content-encoding"].to_s.include?("gzip")

      Zlib::GzipReader.new(StringIO.new(body)).read
    end

    # events is the decoded body's events array, or [] for anything else.
    def events
      JSON.parse(decoded_body)["events"] || []
    rescue JSON::ParserError
      []
    end
  end

  # Stream is one open change stream connection.
  class Stream
    attr_reader :request

    def initialize(socket, request)
      @socket = socket
      @request = request
      @queue = Queue.new
    end

    def write(text) = @queue << [:write, text]
    def close = @queue << [:close]
    def reset = @queue << [:reset]

    # run writes queued frames as chunks until closed or reset.
    def run
      loop do
        op, text = @queue.pop
        case op
        when :write then @socket.write(format("%<size>x\r\n%<text>s\r\n", size: text.bytesize, text: text))
        when :close then return @socket.write("0\r\n\r\n")
        when :reset then return @socket.setsockopt(Socket::SOL_SOCKET, Socket::SO_LINGER, [1, 0].pack("ii"))
        end
      end
    end
  end

  attr_reader :base_url

  def initialize
    @server = TCPServer.new("127.0.0.1", 0)
    @base_url = "http://127.0.0.1:#{@server.addr[1]}"
    @mutex = Mutex.new
    @requests = []
    @streams = []
    @threads = []
    @documents = {}
    @version = 0
    @gzip = false
    @accept = Thread.new { accept_loop }
  end

  # publish makes document the current ruleset and emits its version on
  # every open stream unless quiet.
  def publish(document, quiet: false)
    json = document.is_a?(String) ? document : JSON.generate(document)
    version = JSON.parse(json)["version"]
    @mutex.synchronize do
      @version = version
      @documents[version] = json
    end
    emit(version) unless quiet
    version
  end

  def version = @mutex.synchronize { @version }

  def gzip! = @gzip = true

  # on_ruleset scripts the ruleset endpoint: the block receives the request
  # and returns [status, headers, body], or nil for the default behaviour.
  def on_ruleset(&block) = @mutex.synchronize { @ruleset_script = block }

  # on_stream scripts the stream endpoint the same way; nil opens a
  # default stream.
  def on_stream(&block) = @mutex.synchronize { @stream_script = block }

  def emit(version)
    streams.each { |s| s.write("event: ruleset\nid: #{version}\ndata: {\"version\":#{version}}\n\n") }
  end

  def heartbeat = streams.each { |s| s.write(": hb\n\n") }
  def close_streams = take_streams.each(&:close)
  def reset_streams = take_streams.each(&:reset)
  def streams = @mutex.synchronize { @streams.dup }

  def requests(path = nil)
    all = @mutex.synchronize { @requests.dup }
    path ? all.select { |r| r.path == path } : all
  end

  def fetches = requests("/sdk/v1/ruleset")
  def stream_requests = requests("/sdk/v1/stream")
  def events_requests = requests("/sdk/v1/events")

  # on_events scripts the events endpoint: the block receives the request
  # and returns [status, headers, body], or nil for the default behaviour
  # (202, accepting every event it was sent).
  def on_events(&block) = @mutex.synchronize { @events_script = block }

  def close
    @server.close
    take_streams.each(&:reset)
    @accept.kill
    @mutex.synchronize { @threads.dup }.each(&:kill)
  end

  private

  def take_streams
    @mutex.synchronize do
      s = @streams
      @streams = []
      s
    end
  end

  def accept_loop
    loop do
      socket = @server.accept
      t = Thread.new { serve(socket) }
      @mutex.synchronize { @threads << t }
    end
  rescue IOError, SystemCallError
    nil
  end

  def serve(socket)
    request = read_request(socket)
    return unless request

    @mutex.synchronize { @requests << request }
    case request.path
    when "/sdk/v1/ruleset" then respond(socket, *ruleset(request),
                                        gzip: request["accept-encoding"].to_s.include?("gzip"))
    when "/sdk/v1/stream" then stream(socket, request)
    when "/sdk/v1/events" then respond(socket, *events(request))
    else respond(socket, 404, {}, "")
    end
  rescue IOError, SystemCallError
    nil
  ensure
    begin
      socket.close
    rescue IOError
      nil
    end
  end

  def read_request(socket)
    line = socket.gets("\r\n") or return
    method, target = line.split
    headers = {}
    while (h = socket.gets("\r\n")) && h != "\r\n"
      name, value = h.chomp.split(":", 2)
      headers[name.downcase] = value.strip
    end
    length = Integer(headers["content-length"] || "0", exception: false) || 0
    body = length.positive? ? socket.read(length) : ""
    # A request through a proxy names the origin in an absolute target.
    Request.new(verb: method, path: target.sub(%r{\Ahttps?://[^/]+}, "").split("?").first, headers: headers,
                at: Process.clock_gettime(Process::CLOCK_MONOTONIC), body: body)
  end

  def events(request)
    script = @mutex.synchronize { @events_script }
    scripted = script&.call(request)
    return scripted if scripted

    body = JSON.generate({ "accepted" => request.events.size, "duplicates" => 0, "dropped_rows" => 0,
                           "rejected" => [] })
    [202, { "Content-Type" => "application/json" }, body]
  end

  def ruleset(request)
    script = @mutex.synchronize { @ruleset_script }
    scripted = script&.call(request)
    return scripted if scripted

    version, json = @mutex.synchronize { [@version, @documents[@version]] }
    return [503, {}, ""] unless json
    return [304, { "ETag" => %("#{version}") }, ""] if request["if-none-match"] == %("#{version}")

    [200, { "ETag" => %("#{version}"), "Content-Type" => "application/json" }, json]
  end

  def respond(socket, status, headers, body, gzip: false)
    if @gzip && gzip && status == 200
      io = StringIO.new
      gz = Zlib::GzipWriter.new(io)
      gz.write(body)
      gz.close
      body = io.string
      headers = headers.merge("Content-Encoding" => "gzip")
    end
    head = headers.merge("Content-Length" => body.bytesize.to_s, "Connection" => "close")
    socket.write("HTTP/1.1 #{status} X\r\n#{head.map { |k, v| "#{k}: #{v}\r\n" }.join}\r\n#{body}")
  end

  def stream(socket, request)
    script = @mutex.synchronize { @stream_script }
    scripted = script&.call(request)
    return respond(socket, *scripted) if scripted

    socket.write("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nCache-Control: no-cache\r\n" \
                 "Transfer-Encoding: chunked\r\n\r\n")
    s = Stream.new(socket, request)
    s.write("retry: 3000\n\n")
    current = version
    last = Integer(request["last-event-id"] || "-1", exception: false) || -1
    s.write("event: ruleset\nid: #{current}\ndata: {\"version\":#{current}}\n\n") if current.positive? && last < current
    @mutex.synchronize { @streams << s }
    s.run
  end
end
