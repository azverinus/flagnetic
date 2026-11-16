# frozen_string_literal: true

require "test_helper"

# EventBatch: packing events into batches, and what it does with an event
# that cannot be encoded.
class EventBatchTest < Minitest::Test
  EventBatch = Flagnetic.const_get(:EventBatch)
  IDENTITY = { "name" => "flagnetic-ruby", "version" => "0", "platform" => "server" }.freeze

  def test_pack_leaves_an_event_that_does_not_encode_out_and_yields_it_with_its_fault
    good = [{ "n" => 1 }, { "n" => 2 }, { "n" => 3 }]
    bad = { "n" => Float::NAN }
    yielded = []
    batches = EventBatch.pack([good[0], bad, good[1], good[2]], IDENTITY) { |e, err| yielded << [e, err.class] }
    assert_equal [good], batches
    assert_equal [[bad, JSON::GeneratorError]], yielded
  end

  def test_pack_without_a_block_still_drops_the_event
    batches = EventBatch.pack([{ "n" => 1 }, { "n" => Float::INFINITY }], IDENTITY)
    assert_equal [[{ "n" => 1 }]], batches
  end

  def test_scrub_replaces_invalid_bytes_and_reads_binary_as_utf8
    event = { "a" => "x\xFFy", "b" => "caf\xC3\xA9".b, "c" => ["z\xC3", { "k\xFF" => "v" }], "d" => 7, "e" => nil }
    assert_equal({ "a" => "x�y", "b" => "café", "c" => ["z�", { "k�" => "v" }], "d" => 7,
                   "e" => nil }, EventBatch.scrub(event))
  end

  def test_scrub_transcodes_another_encoding_and_leaves_the_argument_alone
    latin = "caf\xE9".dup.force_encoding("ISO-8859-1")
    bad = "a\xFF"
    scrubbed = EventBatch.scrub({ "s" => latin, "t" => bad })
    assert_equal ["café", "a�"], scrubbed.values_at("s", "t")
    assert_equal Encoding::UTF_8, scrubbed["s"].encoding
    refute_predicate bad, :valid_encoding?
  end
end
