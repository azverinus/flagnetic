# frozen_string_literal: true

require_relative "lib/flagnetic/version"

Gem::Specification.new do |spec|
  spec.name = "flagnetic"
  spec.version = Flagnetic::VERSION
  spec.authors = ["Anatolii Zverev"]
  spec.summary = "Flagnetic feature flags and experiments: server-mode SDK for Ruby"
  spec.description = "Evaluates Flagnetic rulesets locally with the same results as every other Flagnetic evaluator."
  spec.homepage = "https://github.com/azverinus/flagnetic"
  spec.license = "Apache-2.0"
  spec.required_ruby_version = ">= 3.3"

  spec.metadata = {
    "source_code_uri" => "https://github.com/azverinus/flagnetic/tree/main/sdks/ruby",
    "changelog_uri" => "https://github.com/azverinus/flagnetic/blob/main/sdks/ruby/CHANGELOG.md",
    "rubygems_mfa_required" => "true"
  }

  spec.files = Dir["lib/**/*.rb", "README.md", "LICENSE"]
  spec.require_paths = ["lib"]
end
