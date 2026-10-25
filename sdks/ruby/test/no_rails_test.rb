# frozen_string_literal: true

require "test_helper"
require "open3"
require "rbconfig"

# The gem depends on nothing: the Rails integration loads only when Rails
# is loaded before it.
class NoRailsTest < Minitest::Test
  ROOT = File.expand_path("..", __dir__)

  def test_the_gemspec_declares_no_runtime_dependency
    spec = Gem::Specification.load(File.join(ROOT, "flagnetic.gemspec"))
    assert_empty spec.runtime_dependencies
    assert_includes spec.files, "lib/flagnetic/railtie.rb"
  end

  def test_requiring_the_gem_without_rails_loads_no_part_of_rails
    out, err, status = Open3.capture3(RbConfig.ruby, "-I", File.join(ROOT, "lib"), "-e", <<~RUBY)
      require "flagnetic"
      puts [defined?(Flagnetic::Railtie).inspect, defined?(ActiveSupport).inspect,
            $LOADED_FEATURES.grep(%r{/(railties|activesupport|actionpack)-\d}).size].join(" ")
    RUBY
    assert status.success?, err
    assert_equal "nil nil 0", out.chomp
  end
end
