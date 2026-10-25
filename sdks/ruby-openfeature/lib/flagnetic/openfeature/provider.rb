# frozen_string_literal: true

module Flagnetic
  module OpenFeature
    # Provider serves the values of a Flagnetic::Client it builds and owns
    # to the OpenFeature API.
    #
    #   provider = Flagnetic::OpenFeature::Provider.new(ENV.fetch("FLAGNETIC_SDK_KEY"),
    #                                                   base_url: "https://flags.example.com")
    #   OpenFeature::SDK.configure { |config| config.set_provider_and_wait(provider) }
    #   client = OpenFeature::SDK.build_client
    #   client.fetch_boolean_value(flag_key: "new_checkout", default_value: false,
    #                              evaluation_context: OpenFeature::SDK::EvaluationContext.new(targeting_key: "u-1001"))
    #
    # Reasons map as every Flagnetic provider maps them (REASONS), the
    # Flagnetic reason itself in the flag metadata under
    # REASON_METADATA_KEY. Exposures and aliases go through track:
    #
    #   client.track("exposure", evaluation_context: context,
    #                tracking_event_details: OpenFeature::SDK::TrackingEventDetails.new(flag: "pricing"))
    #   client.track("alias", tracking_event_details: OpenFeature::SDK::TrackingEventDetails.new(
    #     fromUnitType: "anonymous", fromUnitId: "d-1", toUnitType: "user", toUnitId: "u-1001"
    #   ))
    #
    # Events: CONFIGURATION_CHANGED with the keys of the flags that may have
    # changed; STALE when the client moves to stage last_good; READY when it
    # is fresh again; ERROR when the SDK key is revoked, kept until the key
    # is accepted again.
    class Provider
      include ::OpenFeature::SDK::Provider::EventEmitter

      NAME = "flagnetic"
      DEFAULT_UNIT_TYPE = "user"
      DEFAULT_READY_TIMEOUT = 10

      # The tracking event names every Flagnetic provider accepts, and the
      # attributes alias reads.
      TRACK_EXPOSURE = "exposure"
      TRACK_ALIAS = "alias"
      ALIAS_ATTRIBUTES = %w[fromUnitType fromUnitId toUnitType toUnitId].freeze
      private_constant :TRACK_EXPOSURE, :TRACK_ALIAS, :ALIAS_ATTRIBUTES

      # Whether the OpenFeature SDK lets a provider announce its own state
      # after init (0.7 and later). Older releases send READY themselves
      # once init returns, whatever the provider found.
      LIFECYCLE_EVENTS = ::OpenFeature::SDK::Configuration.private_method_defined?(:emits_lifecycle_events?)
      private_constant :LIFECYCLE_EVENTS

      # client is the wrapped Flagnetic::Client: local overrides, the
      # recorder of an offline client, flush and status.
      attr_reader :client, :metadata

      # new builds a Flagnetic::Client for sdk_key and base_url with every
      # further option as Flagnetic::Client.new takes it (offline for tests
      # included), and fails exactly when that does.
      #
      # - unit_type: the unit type the targeting key is a unit ID of;
      #   default "user". The units attribute, a Hash of unit type to unit
      #   ID, adds further units; every other attribute is an attribute of
      #   the context.
      # - ready_timeout: seconds init waits for the client's first fetch;
      #   default 10. init never fails: at stage built_in the fallback
      #   chain still serves the built-in defaults.
      # - logger: as Flagnetic::Client.new; it keeps receiving every log
      #   event.
      def initialize(sdk_key = nil, base_url: nil, unit_type: DEFAULT_UNIT_TYPE,
                     ready_timeout: DEFAULT_READY_TIMEOUT, logger: nil, **client_options)
        @unit_type = unit_type.to_s
        @ready_timeout = ready_timeout
        @metadata = ::OpenFeature::SDK::Provider::ProviderMetadata.new(name: NAME).freeze
        @log = Flagnetic::Logging.adapt(logger)
        @initialized = false
        @closed = false
        @revoked = false
        @client = Flagnetic::Client.new(sdk_key, base_url: base_url, logger: method(:observe), **client_options)
        @unsubscribe = [@client.on_change { |keys| changed(keys) }, @client.on_stage { |stage| staged(stage) }]
      end

      # emits_lifecycle_events? tells the OpenFeature SDK that init
      # announces the provider's state itself.
      def emits_lifecycle_events? = true

      # init waits for the client's first fetch, at most ready_timeout
      # seconds, and never raises. Until it returns, stage changes and a
      # revoked key send no event; init then announces what the client
      # reached.
      def init(_evaluation_context = nil)
        @client.ready(timeout: @ready_timeout)
        @initialized = true
        announce if LIFECYCLE_EVENTS
        nil
      end

      # shutdown closes the wrapped client, which reports what is queued
      # one last time; no event is sent after it.
      def shutdown
        @closed = true
        @unsubscribe.each(&:call)
        @client.close
        nil
      end

      def fetch_boolean_value(flag_key:, default_value:, evaluation_context: nil)
        OpenFeature.resolution(detail(flag_key, default_value, evaluation_context))
      end

      def fetch_string_value(flag_key:, default_value:, evaluation_context: nil)
        OpenFeature.resolution(detail(flag_key, default_value, evaluation_context))
      end

      # fetch_number_value serves a number flag as the ruleset has it, an
      # Integer or a Float.
      def fetch_number_value(flag_key:, default_value:, evaluation_context: nil)
        OpenFeature.resolution(detail(flag_key, default_value, evaluation_context))
      end

      # fetch_integer_value serves a number flag as an Integer, as the
      # client's int getter does: a number that is not whole serves
      # default_value with TYPE_MISMATCH rather than a truncated value.
      def fetch_integer_value(flag_key:, default_value:, evaluation_context: nil)
        OpenFeature.resolution(integral(detail(flag_key, default_value, evaluation_context), default_value))
      end

      # fetch_float_value serves a number flag as a Float.
      def fetch_float_value(flag_key:, default_value:, evaluation_context: nil)
        served = detail(flag_key, default_value, evaluation_context)
        served = Flagnetic::Detail.new(**served.to_h, value: served.value.to_f) if served.reason != :fallback
        OpenFeature.resolution(served)
      end

      # fetch_object_value serves a JSON flag's value, parsed and frozen.
      def fetch_object_value(flag_key:, default_value:, evaluation_context: nil)
        OpenFeature.resolution(detail(flag_key, default_value, evaluation_context))
      end

      # track reports the exposure of the flag named by the flag attribute
      # for evaluation_context, as Flagnetic::Client#exposure does, for the
      # event "exposure"; for "alias" it records the alias named by the
      # fromUnitType, fromUnitId, toUnitType and toUnitId attributes. An
      # event naming neither, or missing what its mapping needs, does
      # nothing.
      def track(tracking_event_name, evaluation_context: nil, tracking_event_details: nil)
        fields = tracking_event_details&.fields || {}
        case tracking_event_name
        when TRACK_EXPOSURE
          flag = fields["flag"]
          @client.exposure(flag, context: context(evaluation_context)) if present?(flag)
        when TRACK_ALIAS
          units = fields.values_at(*ALIAS_ATTRIBUTES)
          @client.alias(*units) if units.all? { |unit| present?(unit) }
        end
        nil
      end

      private

      def detail(flag_key, default_value, evaluation_context)
        @client.detail(flag_key, default: default_value, context: context(evaluation_context))
      end

      def context(evaluation_context) = OpenFeature.context(evaluation_context, @unit_type)

      def present?(value) = value.is_a?(String) && !value.empty?

      # integral narrows a served number to an Integer; one that is not
      # whole is a type mismatch, served as the fallback the client's own
      # int getter serves.
      def integral(served, default_value)
        return served if served.reason == :fallback || served.value.is_a?(Integer)

        value = served.value
        if value.is_a?(Float) && value.finite? && value.to_r.denominator == 1
          return Flagnetic::Detail.new(**served.to_h, value: value.to_i)
        end

        Flagnetic::Detail.new(value: default_value, reason: :fallback, error_code: :type_mismatch,
                              ruleset_version: served.ruleset_version, received_at: served.received_at,
                              stage: served.stage)
      end

      # announce sends the state init found: ERROR on a revoked key, else
      # READY, followed by STALE when only a last good ruleset is held.
      def announce
        return error if @revoked

        emit(::OpenFeature::SDK::ProviderEvent::PROVIDER_READY)
        stale if @client.stage == :last_good
      end

      def changed(keys)
        emit(::OpenFeature::SDK::ProviderEvent::PROVIDER_CONFIGURATION_CHANGED, flags_changed: keys)
      end

      # staged sends READY on every move to fresh, so the OpenFeature status
      # leaves STALE or ERROR when the client recovers, and STALE on a move
      # to last_good. A revoked key moves a fresh client to last_good right
      # after the ERROR it sent: STALE would hide the ERROR, so none is sent
      # while the key stays revoked.
      def staged(stage)
        return unless @initialized

        case stage
        when :fresh
          @revoked = false
          emit(::OpenFeature::SDK::ProviderEvent::PROVIDER_READY)
        when :last_good
          stale unless @revoked
        end
      end

      def stale
        emit(::OpenFeature::SDK::ProviderEvent::PROVIDER_STALE, message: "flagnetic: ruleset stage is last_good")
      end

      def error
        emit(::OpenFeature::SDK::ProviderEvent::PROVIDER_ERROR,
             error_code: ::OpenFeature::SDK::Provider::ErrorCode::GENERAL, message: "flagnetic: sdk key revoked")
      end

      # observe is the client's logger: the application's own, plus ERROR
      # when the SDK key is revoked, the one fetch failure stage last_good
      # alone does not tell from an ordinary outage. The event goes first:
      # the client ignores a logger that raises, which would otherwise lose
      # the event as well.
      def observe(level, event, fields)
        if event == :key_revoked
          @revoked = true
          error if @initialized
        end
        @log.call(level, event, fields)
      end

      def emit(event, details = {})
        return if @closed

        emit_event(event, details)
      rescue StandardError => e
        @log.call(:error, :listener_failed, { cause: "#{e.class}: #{e.message}" })
      end
    end
  end
end
