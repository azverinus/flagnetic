# frozen_string_literal: true

require "test_helper"
require "tmpdir"
require "flagnetic/testing"

# The process-wide client: Flagnetic.client, assigned or built on first
# use, and Flagnetic::Testing's with_flags swapping an offline client in
# for a block and restoring the previous one.
class GlobalClientTest < Minitest::Test
  include Flagnetic::Testing::Helpers

  FIXTURE_PATH = File.expand_path("../../../conformance/fixtures/experiments/automatic-exposure.json", __dir__)
  FIXTURE = JSON.parse(File.read(FIXTURE_PATH))

  def setup
    @saved = Flagnetic.__send__(:replace_client, nil, nil)
  end

  def teardown
    Flagnetic.__send__(:replace_client, *@saved)
  end

  def test_client_raises_until_one_is_assigned_or_a_builder_is_set
    error = assert_raises(Flagnetic::NotConfigured) { Flagnetic.client }
    assert_match(/Flagnetic.client/, error.message)
    refute Flagnetic.client?
  end

  def test_an_assigned_client_is_served_to_every_thread
    c = Flagnetic::Testing.offline(values: { "on" => true })
    Flagnetic.client = c
    assert Flagnetic.client?
    assert_same c, Thread.new { Flagnetic.client }.value
  end

  def test_the_builder_runs_once_on_first_use_and_not_before
    built = 0
    Flagnetic.client_builder = lambda {
      built += 1
      Flagnetic::Testing.offline(values: {})
    }
    refute Flagnetic.client?
    assert_equal 0, built
    clients = Array.new(8) { Thread.new { Flagnetic.client } }.map(&:value)
    assert_equal 1, built
    assert_equal 1, clients.uniq.size
    assert Flagnetic.client?
  end

  def test_with_flags_serves_the_values_and_restores_the_previous_client
    previous = Flagnetic::Testing.offline(values: { "new_checkout" => false })
    Flagnetic.client = previous
    with_flags("new_checkout" => true, "banner" => "spring") do |c|
      assert_same c, Flagnetic.client
      assert Flagnetic.client.bool("new_checkout", default: false)
      assert_equal "spring", Flagnetic.client.string("banner", default: "")
      assert_equal :fallthrough, Flagnetic.client.detail("banner", default: "").reason
    end
    assert_same previous, Flagnetic.client
  end

  def test_with_flags_restores_an_unbuilt_client_without_building_it
    built = 0
    Flagnetic.client_builder = lambda {
      built += 1
      Flagnetic::Testing.offline(values: {})
    }
    with_flags("on" => true) { assert Flagnetic.client.bool("on", default: false) }
    refute Flagnetic.client?
    assert_equal 0, built
  end

  def test_with_flags_restores_the_previous_client_when_the_block_raises
    previous = Flagnetic::Testing.offline(values: {})
    Flagnetic.client = previous
    assert_raises(RuntimeError) { with_flags("on" => true) { raise "boom" } }
    assert_same previous, Flagnetic.client
  end

  def test_with_flags_takes_symbol_keys
    with_flags(new_checkout: true) { assert Flagnetic.client.bool("new_checkout", default: false) }
  end

  def test_with_flags_nests
    with_flags("a" => 1) do
      with_flags("a" => 2) { assert_equal 2, Flagnetic.client.number("a", default: 0) }
      assert_equal 1, Flagnetic.client.number("a", default: 0)
    end
  end

  def test_with_ruleset_takes_a_document_and_its_recorder_holds_exposures
    assigned = { units: { "anonymous" => "u_10482" } }
    with_ruleset(FIXTURE["ruleset"]) do |c|
      Flagnetic.client.string("auto_off", default: "", context: assigned)
      Flagnetic.client.exposure("auto_off", context: assigned)
      assert_equal [%w[checkout_v2 control u_10482]],
                   (c.recorder.exposures.map { |e| [e.experiment_key, e.variation_key, e.unit_id] })
    end
  end

  def test_with_ruleset_takes_a_file
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "flags.json"), JSON.generate(FIXTURE["ruleset"]))
      with_ruleset(file: File.join(dir, "flags.json")) do
        refute_equal :fallback, Flagnetic.client.detail("auto_on", default: "").reason
      end
    end
  end

  def test_with_flags_needs_a_block
    assert_raises(ArgumentError) { with_flags("on" => true) }
  end
end
