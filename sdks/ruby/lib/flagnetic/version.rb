# frozen_string_literal: true

module Flagnetic
  VERSION = "0.1.0"

  # The schema_version range of the documents this SDK reads and the SDK API
  # path it speaks. A ruleset outside the range is a fallback chain failure,
  # logged once as schema_unsupported; every ruleset and events request asks
  # for SCHEMA_VERSION_MAX in Flagnetic-Schema-Version (the change stream
  # carries no document). A change to any of these needs a row in
  # docs/sdks/compatibility.md, and a new SDK_API_PATH is a new major
  # VERSION.
  SCHEMA_VERSION_MIN = 1
  SCHEMA_VERSION_MAX = 1
  SDK_API_PATH = "/sdk/v1"
end
