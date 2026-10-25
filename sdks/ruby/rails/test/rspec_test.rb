# frozen_string_literal: true

require_relative "test_helper"

# The block helpers under RSpec, run as RSpec runs: its own process.
class RSpecTest < Minitest::Test
  def test_the_block_helpers_work_in_rspec_examples
    spec = File.expand_path("spec/with_flags_spec.rb", __dir__)
    out, err, status = Open3.capture3({ "BUNDLE_GEMFILE" => GEMFILE }, "bundle", "exec", "rspec", spec,
                                      chdir: File.expand_path("..", __dir__))
    assert status.success?, "#{out}\n#{err}"
    assert_match(/2 examples, 0 failures/, out)
  end
end
