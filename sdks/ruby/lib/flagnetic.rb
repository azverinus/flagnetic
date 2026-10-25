# frozen_string_literal: true

require_relative "flagnetic/version"
require_relative "flagnetic/eval"
require_relative "flagnetic/detail"
require_relative "flagnetic/logging"
require_relative "flagnetic/backoff"
require_relative "flagnetic/sse_parser"
require_relative "flagnetic/transport"
require_relative "flagnetic/stream"
require_relative "flagnetic/changes"
require_relative "flagnetic/recorder"
require_relative "flagnetic/store"
require_relative "flagnetic/local_overrides"
require_relative "flagnetic/uuid"
require_relative "flagnetic/generations"
require_relative "flagnetic/event_batch"
require_relative "flagnetic/reporter"
require_relative "flagnetic/exposures"
require_relative "flagnetic/summaries"
require_relative "flagnetic/worker"
require_relative "flagnetic/fallback_chain"
require_relative "flagnetic/client"
require_relative "flagnetic/global"
require_relative "flagnetic/railtie" if defined?(Rails::Railtie)

# Flagnetic is the Ruby SDK for Flagnetic feature flags and experiments:
# server mode, local evaluation from a ruleset held in memory. Start with
# Flagnetic::Client, or Flagnetic.client for the process-wide one; tests
# use Flagnetic::Testing (require "flagnetic/testing"). In a Rails app the
# railtie builds Flagnetic.client from configuration; the gem itself
# depends on nothing outside the standard library, Rails included.
module Flagnetic
end
