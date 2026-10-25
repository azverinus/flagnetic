# frozen_string_literal: true

ENV["RAILS_ENV"] = "test"
ENV.delete("FLAGNETIC_SDK_KEY")
ENV.delete("FLAGNETIC_BASE_URL")
require_relative "../app/config/environment"
require "flagnetic/testing"

RSpec.configure { |config| config.include Flagnetic::Testing::Helpers }

FIXTURE = JSON.parse(File.read(File.expand_path(
                                 "../../../../../conformance/fixtures/experiments/automatic-exposure.json", __dir__
                               )))

RSpec.describe "Flagnetic::Testing::Helpers under RSpec" do
  let(:app) { ActionDispatch::Integration::Session.new(Rails.application) }

  it "serves the values to a request and restores the application's client" do
    with_flags(new_checkout: true) do
      app.get "/flags/new_checkout"
      expect(JSON.parse(app.response.body)).to include("value" => true, "reason" => "fallthrough")
    end
    app.get "/flags/new_checkout"
    expect(JSON.parse(app.response.body)).to include("value" => false, "reason" => "fallback")
  end

  it "reaches the recorder to assert an exposure" do
    app.cookies["flagnetic_anonymous_id"] = "u_10482"
    with_ruleset(FIXTURE["ruleset"]) do |client|
      app.post "/flags/auto_off/exposure"
      expect(client.recorder.exposures.map(&:experiment_key)).to eq(["checkout_v2"])
    end
  end
end
