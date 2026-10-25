# frozen_string_literal: true

require_relative "lib/flagnetic/openfeature/version"

Gem::Specification.new do |spec|
  spec.name = "flagnetic-openfeature"
  spec.version = Flagnetic::OpenFeature::VERSION
  spec.authors = ["Anatolii Zverev"]
  spec.summary = "OpenFeature provider for Flagnetic"
  spec.description = "Serves Flagnetic flags through the OpenFeature API, with exposures and aliases through track."
  spec.homepage = "https://github.com/azverinus/flagnetic"
  spec.license = "Apache-2.0"
  spec.required_ruby_version = ">= 3.4"

  spec.metadata = {
    "source_code_uri" => "https://github.com/azverinus/flagnetic/tree/main/sdks/ruby-openfeature",
    "changelog_uri" => "https://github.com/azverinus/flagnetic/blob/main/sdks/ruby-openfeature/CHANGELOG.md",
    "rubygems_mfa_required" => "true"
  }

  spec.files = Dir["lib/**/*.rb", "README.md", "LICENSE"]
  spec.require_paths = ["lib"]

  spec.add_dependency "flagnetic", "~> 0.1"
  spec.add_dependency "openfeature-sdk", "~> 0.6", ">= 0.6.1"
end
