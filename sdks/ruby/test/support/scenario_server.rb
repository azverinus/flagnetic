# frozen_string_literal: true

require "socket"
require "zlib"
require "stringio"

# ScenarioServer is the mock server of conformance/protocol/scenarios.json:
# it holds every request until the scenario runner answers it, so each
# step decides what the SDK receives. Requests queue by path, so a request
# to another path waits for its own step.
class ScenarioServer
  Request = Struct.new(:verb, :path, :headers, :at, :socket, :body, keyword_init: true) do
    def [](name) = headers[name.downcase]

    # decoded_body ungzips body when Content-Encoding says so; POST
    # /sdk/v1/events bodies always are.
    def decoded_body
      return body unless self["content-encoding"].to_s.include?("gzip")

      Zlib::GzipReader.new(StringIO.new(body)).read
    end
  end

  attr_reader :base_url

  def initialize
    @server = TCPServer.new("127.0.0.1", 0)
    @base_url = "http://127.0.0.1:#{@server.addr[1]}"
    @mutex = Mutex.new
    @arrived = ConditionVariable.new
    @inbox = []
    @open = []
    @stream = nil
    @accept = Thread.new { accept_loop }
  end

  def now = Process.clock_gettime(Process::CLOCK_MONOTONIC)

  # take waits up to timeout seconds for the next request to path and
  # returns it, or nil.
  def take(path, timeout)
    deadline = now + timeout
    @mutex.synchronize do
      loop do
        i = @inbox.index { |r| path.nil? || r.path == path }
        return @inbox.delete_at(i) if i

        left = deadline - now
        return nil unless left.positive?

        @arrived.wait(@mutex, left)
      end
    end
  end

  # pending returns the requests nobody has taken, optionally to path.
  def pending(path = nil) = @mutex.synchronize { @inbox.select { |r| path.nil? || r.path == path } }

  # respond answers a request as a scripted response of the scenario list:
  # a network error drops the connection, stream keeps it open as the
  # change stream.
  def respond(request, response, body)
    socket = request.socket
    return socket.close if response["network_error"]

    headers = (response["headers"] || {}).reject { |k, _| k.casecmp?("content-length") }
    if response["stream"]
      write_head(socket, response["status"], headers.merge("Transfer-Encoding" => "chunked"))
      @mutex.synchronize { @stream = socket }
    else
      write_head(socket, response["status"], headers.merge("Content-Length" => body.bytesize.to_s,
                                                           "Connection" => "close"))
      socket.write(body)
      socket.close
    end
  end

  def stream_send(text)
    socket = @mutex.synchronize { @stream } or raise "no open stream"
    socket.write(format("%<size>x\r\n%<text>s\r\n", size: text.bytesize, text: text))
  end

  def stream_close
    socket = @mutex.synchronize { @stream.tap { @stream = nil } } or raise "no open stream"
    socket.write("0\r\n\r\n")
    socket.close
  end

  def close
    @server.close
    @accept.kill
    @mutex.synchronize { @open.dup }.each do |s|
      s.close
    rescue IOError
      nil
    end
  end

  private

  def write_head(socket, status, headers)
    socket.write("HTTP/1.1 #{status} X\r\n#{headers.map { |k, v| "#{k}: #{v}\r\n" }.join}\r\n")
  end

  def accept_loop
    loop do
      socket = @server.accept
      Thread.new { read(socket) }
    end
  rescue IOError, SystemCallError
    nil
  end

  def read(socket)
    line = socket.gets("\r\n") or return socket.close
    verb, target = line.split
    headers = {}
    while (h = socket.gets("\r\n")) && h != "\r\n"
      name, value = h.chomp.split(":", 2)
      headers[name.downcase] = value.strip
    end
    length = Integer(headers["content-length"] || "0", exception: false) || 0
    body = length.positive? ? socket.read(length) : ""
    request = Request.new(verb: verb, path: target.split("?").first, headers: headers, at: now, socket: socket,
                          body: body)
    @mutex.synchronize do
      @open << socket
      @inbox << request
      @arrived.broadcast
    end
  rescue IOError, SystemCallError
    nil
  end
end
