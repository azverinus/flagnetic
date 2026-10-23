# frozen_string_literal: true

require "test_helper"

# The schema range this SDK reads and the SDK API path it speaks, which
# docs/sdks/compatibility.md lists per SDK line.
class VersionTest < Minitest::Test
  def test_the_schema_range_and_the_sdk_api_path_are_declared
    assert_equal 1, Flagnetic::SCHEMA_VERSION_MIN
    assert_equal 1, Flagnetic::SCHEMA_VERSION_MAX
    assert_equal "/sdk/v1", Flagnetic::SDK_API_PATH
  end

  # The evaluator compiles one schema; the SDK cannot read another.
  def test_the_highest_schema_is_the_evaluators
    assert_equal Flagnetic::Eval::SCHEMA_VERSION, Flagnetic::SCHEMA_VERSION_MAX
  end
end
