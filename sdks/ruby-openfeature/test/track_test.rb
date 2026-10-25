# frozen_string_literal: true

require "test_helper"

# TrackTest covers exposure and alias through OpenFeature's track, the only
# way they are recorded: evaluating never records an exposure by itself.
class TrackTest < Minitest::Test
  include ProviderHelper

  def details(**fields) = OF::TrackingEventDetails.new(**fields)

  def test_exposure_through_track
    provider, client = of_provider
    client.fetch_string_value(flag_key: "pricing", default_value: "x", evaluation_context: user)
    client.track("exposure", evaluation_context: user, tracking_event_details: details(flag: "pricing"))
    exposures = provider.client.recorder.exposures
    assert_equal 1, exposures.size
    assert_equal %w[pricing_test treatment user u-1],
                 [exposures.first.experiment_key, exposures.first.variation_key, exposures.first.unit_type,
                  exposures.first.unit_id]
  end

  def test_exposure_of_a_flag_without_an_experiment_records_nothing
    provider, client = of_provider
    client.fetch_boolean_value(flag_key: "checkout", default_value: false, evaluation_context: user(country: "DE"))
    client.track("exposure", evaluation_context: user(country: "DE"), tracking_event_details: details(flag: "checkout"))
    assert_empty provider.client.recorder.exposures
  end

  def test_evaluation_alone_never_records_an_exposure
    provider, client = of_provider
    client.fetch_string_value(flag_key: "pricing", default_value: "x", evaluation_context: user)
    assert_empty provider.client.recorder.exposures
  end

  def test_automatic_exposure_needs_no_track
    provider, client = of_provider
    client.fetch_string_value(flag_key: "auto_flag", default_value: "x", evaluation_context: user)
    assert_equal ["auto_test"], provider.client.recorder.exposures.map(&:experiment_key)
  end

  def test_alias_through_track
    provider, client = of_provider
    client.track("alias", tracking_event_details: details(fromUnitType: "anonymous", fromUnitId: "d-1",
                                                          toUnitType: "user", toUnitId: "u-1"))
    aliases = provider.client.recorder.aliases
    assert_equal 1, aliases.size
    assert_equal %w[anonymous d-1 user u-1],
                 [aliases.first.from_unit_type, aliases.first.from_unit_id, aliases.first.to_unit_type,
                  aliases.first.to_unit_id]
  end

  def test_track_without_what_its_mapping_needs_does_nothing
    provider, client = of_provider
    client.fetch_string_value(flag_key: "pricing", default_value: "x", evaluation_context: user)
    client.track("exposure", evaluation_context: user)
    client.track("exposure", evaluation_context: user, tracking_event_details: details(flag: ""))
    client.track("exposure", evaluation_context: user, tracking_event_details: details(flag: 1))
    client.track("alias", tracking_event_details: details(fromUnitType: "anonymous", fromUnitId: "d-1",
                                                          toUnitType: "user"))
    client.track("alias", tracking_event_details: details(fromUnitType: "anonymous", fromUnitId: "",
                                                          toUnitType: "user", toUnitId: "u-1"))
    client.track("purchase", evaluation_context: user, tracking_event_details: details(value: 9.99, flag: "pricing"))
    assert_empty provider.client.recorder.exposures
    assert_empty provider.client.recorder.aliases
  end
end
