# frozen_string_literal: true

$LOAD_PATH.unshift(File.expand_path("../../lib", __dir__))
require "json"
require "flagnetic/testing"

RSpec.configure { |config| config.include Flagnetic::Testing::Helpers }

FIXTURE = JSON.parse(File.read(File.expand_path("../../../../conformance/fixtures/experiments/automatic-exposure.json",
                                                __dir__)))

RSpec.describe "Flagnetic::Testing::Helpers under RSpec" do
  it "serves the values for the block and restores the previous client" do
    previous = Flagnetic::Testing.offline(values: { "new_checkout" => false })
    Flagnetic.client = previous
    with_flags(new_checkout: true) do |client|
      expect(Flagnetic.client).to be(client)
      expect(Flagnetic.client.bool("new_checkout", default: false)).to be(true)
    end
    expect(Flagnetic.client).to be(previous)
  end

  it "reaches the recorder to assert an exposure" do
    assigned = { units: { "anonymous" => "u_10482" } }
    with_ruleset(FIXTURE["ruleset"]) do |client|
      Flagnetic.client.string("auto_off", default: "", context: assigned)
      Flagnetic.client.exposure("auto_off", context: assigned)
      expect(client.recorder.exposures.map(&:experiment_key)).to eq(["checkout_v2"])
    end
  end
end
