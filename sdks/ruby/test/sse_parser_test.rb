# frozen_string_literal: true

require "test_helper"

# The ruleset stream's parser follows the WHATWG event-stream rules: any of
# CRLF, LF or CR ends a line, even split across reads.
class SSEParserTest < Minitest::Test
  SSEParser = Flagnetic.const_get(:SSEParser)

  def parse(*chunks)
    parser = SSEParser.new
    out = []
    chunks.each { |c| parser.feed(c) { |kind, value| out << [kind, value] } }
    out
  end

  def event(type, data, id = nil)
    [:event, SSEParser::Event.new(type: type, data: data, id: id)]
  end

  def test_the_frames_the_server_writes
    out = parse("retry: 3000\n\n", "event: ruleset\nid: 7\ndata: {\"version\":7}\n\n", ": hb\n\n")
    assert_equal [event("ruleset", '{"version":7}', "7"), [:comment, " hb"]], out
  end

  def test_crlf_cr_and_lf_line_endings
    assert_equal [event("ruleset", "a", "1")], parse("event: ruleset\r\nid: 1\r\ndata: a\r\n\r\n")
    assert_equal [event("ruleset", "a", "1")], parse("event: ruleset\rid: 1\rdata: a\r\r")
  end

  def test_a_crlf_split_between_reads_is_one_line_end
    assert_equal [event("message", "a")], parse("data: a\r", "\n\r", "\n")
  end

  def test_frames_split_anywhere
    text = "event: ruleset\nid: 12\ndata: {\"version\":12}\n\n"
    chunks = text.chars
    assert_equal [event("ruleset", '{"version":12}', "12")], parse(*chunks)
  end

  def test_multi_line_data_joins_with_newlines
    assert_equal [event("message", "a\nb\n")], parse("data: a\ndata: b\ndata:\n\n")
  end

  def test_one_leading_space_is_stripped_and_a_field_without_colon_is_empty
    assert_equal [event("message", " x\n")], parse("data:  x\ndata\n\n")
  end

  def test_the_last_event_id_carries_over_and_an_id_with_nul_is_ignored
    out = parse("id: 3\ndata: a\n\n", "data: b\n\n", "id: 4\u00005\ndata: c\n\n")
    assert_equal [event("message", "a", "3"), event("message", "b", "3"), event("message", "c", "3")], out
  end

  def test_an_event_without_data_is_not_dispatched_and_resets_the_type
    assert_equal [event("message", "x")], parse("event: ruleset\n\n", "data: x\n\n")
  end

  def test_a_retry_and_unknown_fields_are_ignored
    assert_empty parse("retry: 1x\nretry: 10\nfoo: bar\n\n")
  end

  def test_a_leading_bom_is_skipped
    assert_equal [event("message", "a")], parse("\xEF\xBB\xBFdata: a\n\n".b)
  end

  def test_an_unterminated_line_waits_for_more
    assert_empty parse("data: a\n")
  end

  def test_an_endless_line_is_refused
    parser = SSEParser.new(max_line: 16)
    assert_raises(SSEParser::Error) { parser.feed("x" * 17) { nil } }
  end

  def test_the_default_line_limit_is_64_kib
    parser = SSEParser.new
    parser.feed("data: #{"x" * ((64 << 10) - 6)}") { nil } # exactly at the limit, still waiting for its end
    assert_raises(SSEParser::Error) { parser.feed("x") { nil } }
  end

  def test_a_line_without_a_newline_arriving_in_pieces_is_refused
    parser = SSEParser.new(max_line: 64)
    pieces = 0
    error = assert_raises(SSEParser::Error) do
      loop do
        parser.feed("x" * 10) { nil }
        pieces += 1
      end
    end
    assert_match(/exceeds 64 bytes/, error.message)
    assert_operator pieces, :<=, 7
  end

  def test_endless_short_data_lines_are_refused
    parser = SSEParser.new(max_line: 64, max_event: 256)
    error = assert_raises(SSEParser::Error) do
      100.times { parser.feed("data: #{"y" * 30}\n") { nil } }
    end
    assert_match(/event exceeds 256 bytes/, error.message)
  end

  def test_an_event_exactly_at_the_limit_dispatches_and_the_count_restarts
    parser = SSEParser.new(max_line: 64, max_event: 8)
    out = []
    parser.feed("data: abc\ndata: de\n\n") { |k, v| out << [k, v] } # "abc\nde\n" is 7 bytes of data plus line ends
    parser.feed("data: abc\ndata: de\n\n") { |k, v| out << [k, v] }
    assert_equal 2, out.size
  end

  def test_one_large_chunk_of_short_data_lines_is_refused
    parser = SSEParser.new(max_line: 64, max_event: 256)
    assert_raises(SSEParser::Error) { parser.feed("data: #{"y" * 30}\n" * 100) { nil } }
  end
end
