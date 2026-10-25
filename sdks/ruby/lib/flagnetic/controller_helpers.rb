# frozen_string_literal: true

require "securerandom"
require_relative "version"

module Flagnetic
  # ControllerHelpers builds the context of the current request; the
  # railtie includes it in every controller and makes flagnetic_context a
  # view helper.
  #
  #   Flagnetic.client.bool("new_checkout", default: false, context: flagnetic_context)
  #
  # The context holds the configured unit types:
  #
  # - config.flagnetic.user_unit_type: the unit type of the signed-in user,
  #   whose ID config.flagnetic.user_id reads (a callable receiving the
  #   controller; by default current_user&.id where the controller has
  #   current_user, else session[:user_id]). Absent while signed out.
  # - config.flagnetic.anonymous_unit_type: the unit type of a random ID
  #   generated on the first request and kept in the cookie named by
  #   config.flagnetic.anonymous_cookie. Signing in keeps it, so the context
  #   of a signed-in user carries both IDs and an experiment spanning
  #   sign-up keeps its variation; call Flagnetic.client.alias
  #   with flagnetic_anonymous_id at sign-up to link them for analysis.
  #
  # Neither is set by default: the SDK never invents a unit type. The
  # attributes are the predeclared sdk and sdk_version, platform and
  # app_version when configured, and whatever config.flagnetic.attributes
  # (a callable receiving the controller) returns, which wins over them.
  module ControllerHelpers
    # SIGNED_IN_USER_ID is the default config.flagnetic.user_id.
    SIGNED_IN_USER_ID = lambda { |controller|
      if controller.respond_to?(:current_user, true)
        controller.__send__(:current_user)&.id
      else
        controller.session[:user_id]
      end
    }

    # ANONYMOUS_ID_FORMAT is what a kept anonymous ID may look like; anything else
    # in the cookie is replaced by a new one.
    ANONYMOUS_ID_FORMAT = /\A[A-Za-z0-9_-]{1,64}\z/

    def self.included(base)
      base.helper_method(:flagnetic_context, :flagnetic_anonymous_id) if base.respond_to?(:helper_method)
    end

    # flagnetic_context returns the context of the current request, built
    # once per request.
    def flagnetic_context
      @flagnetic_context ||= { units: flagnetic_units(flagnetic_config),
                               attributes: flagnetic_attributes(flagnetic_config) }.freeze
    end

    # flagnetic_anonymous_id returns the anonymous ID of the current
    # visitor, generating it and setting its cookie on the first request,
    # or nil when no anonymous unit type is configured.
    def flagnetic_anonymous_id
      return @flagnetic_anonymous_id if defined?(@flagnetic_anonymous_id)

      config = flagnetic_config
      @flagnetic_anonymous_id = (flagnetic_cookie_id(config.anonymous_cookie) if config.anonymous_unit_type)
    end

    private

    def flagnetic_config = ::Rails.application.config.flagnetic

    def flagnetic_units(config)
      units = {}
      units[config.anonymous_unit_type.to_s] = flagnetic_anonymous_id if config.anonymous_unit_type
      if config.user_unit_type
        id = config.user_id.call(self)
        units[config.user_unit_type.to_s] = id.to_s if id.present?
      end
      units
    end

    def flagnetic_attributes(config)
      attributes = { "sdk" => "flagnetic-ruby", "sdk_version" => VERSION }
      attributes["platform"] = config.platform if config.platform
      attributes["app_version"] = config.app_version if config.app_version
      own = config.attributes&.call(self) || {}
      attributes.merge(own.to_h.transform_keys(&:to_s))
    end

    def flagnetic_cookie_id(name)
      jar = request.cookie_jar
      id = jar[name]
      return id if id.is_a?(String) && id.match?(ANONYMOUS_ID_FORMAT)

      id = SecureRandom.uuid
      jar.permanent[name] = { value: id, httponly: true, same_site: :lax, secure: request.ssl? }
      id
    end
  end
end
