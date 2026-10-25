# frozen_string_literal: true

require_relative "boot"
require "logger"
require "json"
require "action_controller/railtie"
require "flagnetic"

# TestApp is the smallest Rails application the integration runs in. The
# test suite boots it in-process for the controller helper and as
# subprocesses (plain boot, console, runner, rake, Puma) for the railtie;
# FLAGNETIC_TEST_* variables steer it from there.
class TestApp < Rails::Application
  config.load_defaults Rails::VERSION::STRING.to_f
  config.root = File.expand_path("..", __dir__)
  config.eager_load = false
  config.secret_key_base = "0123456789abcdef" * 8
  config.hosts.clear
  config.logger = Logger.new($stderr, level: :warn)
  config.log_level = :warn

  config.flagnetic.user_unit_type = "user"
  config.flagnetic.anonymous_unit_type = "anonymous"
  config.flagnetic.platform = "web"
  config.flagnetic.app_version = "2.3.0"
  config.flagnetic.attributes = ->(controller) { { "plan" => controller.request.headers["X-Plan"] }.compact }

  config.flagnetic.start = true if ENV["FLAGNETIC_TEST_START"]
  config.flagnetic.sdk_key = ENV.fetch("FLAGNETIC_TEST_CONFIG_SDK_KEY", nil)
  config.flagnetic.client_options = { allow_overrides: true } if ENV["FLAGNETIC_TEST_CLIENT_ALLOWS_OVERRIDES"]

  if (credentials = ENV.fetch("FLAGNETIC_TEST_CREDENTIALS", nil))
    config.credentials.content_path = "#{credentials}.yml.enc"
    config.credentials.key_path = "#{credentials}.key"
  end

  # Every log event as one JSON line, for the subprocess tests to read.
  if (log = ENV.fetch("FLAGNETIC_TEST_LOG", nil))
    config.flagnetic.logger = lambda { |level, event, fields|
      File.open(log, "a") { |f| f.puts(JSON.generate({ pid: Process.pid, level: level, event: event, **fields })) }
    }
  end
end
