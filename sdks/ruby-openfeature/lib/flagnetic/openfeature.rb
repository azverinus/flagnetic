# frozen_string_literal: true

require "flagnetic"
require "open_feature/sdk"

require_relative "openfeature/version"
require_relative "openfeature/mapping"
require_relative "openfeature/provider"

module Flagnetic
  # OpenFeature serves Flagnetic flags through the OpenFeature API. The
  # flagnetic gem stays free of the OpenFeature dependency; an application
  # on OpenFeature adds this gem beside it. Start with Provider.
  module OpenFeature
  end
end
