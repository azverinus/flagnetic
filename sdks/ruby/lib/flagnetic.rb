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
require_relative "flagnetic/store"
require_relative "flagnetic/local_overrides"
require_relative "flagnetic/worker"
require_relative "flagnetic/fallback_chain"
require_relative "flagnetic/client"

# Flagnetic is the Ruby SDK for Flagnetic feature flags and experiments:
# server mode, local evaluation from a ruleset held in memory. Start with
# Flagnetic::Client. The gem depends on nothing outside the standard
# library.
module Flagnetic
end
