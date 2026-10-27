# frozen_string_literal: true

require "test_helper"

class VersionTest < Minitest::Test
  # The release job sets FLAGNETIC_RELEASE_VERSION to the version of the tag
  # it publishes, so a gem whose version is not the tag's is never pushed;
  # the gemspec takes its version from VERSION.
  def test_the_version_is_the_release_tags
    skip "not a release run" unless ENV.key?("FLAGNETIC_RELEASE_VERSION")
    assert_equal ENV.fetch("FLAGNETIC_RELEASE_VERSION"), Flagnetic::OpenFeature::VERSION
  end
end
