# frozen_string_literal: true

module Flagnetic
  # SSEParser reads the text/event-stream format of the change stream by the
  # WHATWG rules: CRLF, LF or CR end a line, even when split between reads;
  # a blank line dispatches the event gathered so far; lines starting with
  # a colon are comments; data lines join with newlines; the last event ID
  # carries over between events. The server only ever writes `retry`, a
  # `ruleset` event and `: hb` heartbeats, but a proxy may reframe them.
  class SSEParser
    # Event is one dispatched event. type defaults to "message"; id is the
    # last event ID seen, or nil.
    Event = Struct.new(:type, :data, :id, keyword_init: true)

    # Error is a stream the parser refuses: a line longer than max_line.
    class Error < StandardError; end

    BOM = "\xEF\xBB\xBF".b.freeze
    LINE_END = /[\r\n]/n
    private_constant :BOM, :LINE_END

    def initialize(max_line: 1 << 20)
      @max_line = max_line
      @buffer = +"".b
      @skip_lf = false
      @started = false
      @type = +""
      @data = +""
      @id = nil
    end

    # feed parses the next bytes of the stream, yielding [:event, Event],
    # [:retry, milliseconds] and [:comment, text] as they complete.
    def feed(chunk, &)
      bytes = chunk.b
      if @skip_lf && !bytes.empty?
        bytes = bytes.byteslice(1..) if bytes.getbyte(0) == 10
        @skip_lf = false
      end
      @buffer << bytes
      strip_bom
      while (i = @buffer.index(LINE_END))
        line = @buffer.byteslice(0, i)
        @buffer = @buffer.byteslice((i + line_end_length(i))..)
        line(line.force_encoding(Encoding::UTF_8).scrub, &)
      end
      raise Error, "an event-stream line exceeds #{@max_line} bytes" if @buffer.bytesize > @max_line
    end

    private

    def strip_bom
      return if @started || @buffer.bytesize < BOM.bytesize

      @started = true
      @buffer = @buffer.byteslice(BOM.bytesize..) if @buffer.start_with?(BOM)
    end

    # line_end_length is 2 for CRLF, else 1. A CR ending the buffer may be
    # the first half of a CRLF: the LF is skipped when it arrives.
    def line_end_length(index)
      return 1 unless @buffer.getbyte(index) == 13
      return 2 if @buffer.getbyte(index + 1) == 10

      @skip_lf = index + 1 == @buffer.bytesize
      1
    end

    def line(line)
      return dispatch { |e| yield :event, e } if line.empty?
      return yield(:comment, line[1..]) if line.start_with?(":")

      field, value = line.split(":", 2)
      value = value.to_s.delete_prefix(" ")
      case field
      when "event" then @type = value
      when "data" then @data << value << "\n"
      when "id" then @id = value unless value.include?("\u0000")
      when "retry" then yield :retry, value.to_i if value.match?(/\A\d+\z/)
      end
    end

    def dispatch
      data = @data
      type = @type
      @data = +""
      @type = +""
      return if data.empty?

      yield Event.new(type: type.empty? ? "message" : type, data: data.chomp, id: @id)
    end
  end
end
