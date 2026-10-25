# frozen_string_literal: true

require_relative "test_helper"

ENV["RAILS_ENV"] = "test"
ENV.delete("FLAGNETIC_SDK_KEY")
ENV.delete("FLAGNETIC_BASE_URL")
require_relative "app/config/environment"
require "rails/test_help"
require "flagnetic/testing"

# The controller helper and the Minitest block helpers, in-process through
# Rails' own integration test.
class ControllerTest < ActionDispatch::IntegrationTest
  include Flagnetic::Testing::Helpers

  FIXTURE = JSON.parse(File.read(File.expand_path(
                                   "../../../../conformance/fixtures/experiments/automatic-exposure.json", __dir__
                                 )))

  def test_the_context_carries_the_predeclared_attributes_and_the_applications_own
    get "/context", headers: { "X-Plan" => "pro" }
    attributes = response.parsed_body["attributes"]
    assert_equal({ "sdk" => "flagnetic-ruby", "sdk_version" => Flagnetic::VERSION, "platform" => "web",
                   "app_version" => "2.3.0", "plan" => "pro" }, attributes)
  end

  def test_an_anonymous_visitor_gets_an_anonymous_id_kept_in_a_cookie
    get "/context"
    anonymous = response.parsed_body.dig("units", "anonymous")
    assert_match(/\A\h{8}-\h{4}-\h{4}-\h{4}-\h{12}\z/, anonymous)
    assert_equal({ "anonymous" => anonymous }, response.parsed_body["units"])
    assert_match(/flagnetic_anonymous_id=#{anonymous}.*httponly/i, response.headers["Set-Cookie"].to_s)

    get "/context"
    assert_equal anonymous, response.parsed_body.dig("units", "anonymous"), "the same ID on the next request"
  end

  def test_the_anonymous_id_is_kept_after_sign_in_beside_the_user_id
    get "/context"
    anonymous = response.parsed_body.dig("units", "anonymous")
    post "/sign_in", params: { user_id: 1001 }
    get "/context"
    assert_equal({ "anonymous" => anonymous, "user" => "1001" }, response.parsed_body["units"])
  end

  def test_a_malformed_anonymous_cookie_is_replaced
    cookies["flagnetic_anonymous_id"] = "x" * 200
    get "/context"
    anonymous = response.parsed_body.dig("units", "anonymous")
    assert_match(/\A\h{8}-/, anonymous)
  end

  def test_with_flags_serves_the_values_to_the_controller
    with_flags("new_checkout" => true) do
      get "/flags/new_checkout"
      assert_equal [true, "fallthrough"], response.parsed_body.values_at("value", "reason")
    end
    get "/flags/new_checkout"
    assert_equal [false, "fallback"], response.parsed_body.values_at("value", "reason"),
                 "the application's own client is back"
  end

  def test_with_ruleset_records_the_exposure_the_controller_reported
    cookies["flagnetic_anonymous_id"] = "u_10482"
    with_ruleset(FIXTURE["ruleset"]) do |client|
      post "/flags/auto_off/exposure"
      assert_response :no_content
      exposures = client.recorder.exposures
      assert_equal [%w[checkout_v2 auto_off control anonymous u_10482]],
                   (exposures.map { |e| [e.experiment_key, e.flag_key, e.variation_key, e.unit_type, e.unit_id] })
      assert_equal ["web", "2.3.0"], [exposures.first.platform, exposures.first.app_version]
    end
  end
end
