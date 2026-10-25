# frozen_string_literal: true

require "json"
require "open3"
require "tmpdir"
require "minitest/autorun"
require_relative "../../test/support/mock_server"

APP = File.expand_path("app", __dir__)
GEMFILE = ENV.fetch("BUNDLE_GEMFILE", File.expand_path("../Gemfile", __dir__))

# RulesetDocuments builds the ruleset documents the mock server serves.
module RulesetDocuments
  # document is a ruleset whose boolean flag "new_checkout" serves on to
  # everyone.
  def document(version, enabled: true)
    {
      "schema_version" => 1, "version" => version, "scope" => "server", "unit_types" => %w[user anonymous],
      "environment" => { "key" => "staging", "protected" => false },
      "flags" => [{
        "key" => "new_checkout", "type" => "boolean", "on" => true,
        "variations" => [{ "key" => "on", "value" => true }, { "key" => "off", "value" => false }],
        "off_variation" => "off", "fallthrough" => enabled ? "on" : "off"
      }]
    }
  end
end

# AppProcess runs the test application in a subprocess, the way an
# application runs: a plain boot as a server or job process would, rails
# console, rails runner, rake, or Puma in cluster mode.
module AppProcess
  include RulesetDocuments

  def setup
    super
    @server = MockServer.new
    @server.publish(document(1))
    @dir = Dir.mktmpdir("flagnetic-rails")
    @log = File.join(@dir, "flagnetic.log")
  end

  def teardown
    @server.close
    FileUtils.remove_entry(@dir)
    super
  end

  # env is the subprocess environment: the application configured through
  # the two environment variables against the mock server, unless
  # overridden.
  def env(**overrides)
    {
      "BUNDLE_GEMFILE" => GEMFILE, "RAILS_ENV" => "development", "FLAGNETIC_SDK_KEY" => "fn_srv_test",
      "FLAGNETIC_BASE_URL" => @server.base_url, "FLAGNETIC_TEST_LOG" => @log, "FLAGNETIC_TEST_CREDENTIALS" => nil
    }.merge(overrides.transform_keys(&:to_s))
  end

  # run_app runs command in the application directory and returns its
  # standard output, failing the test when it exits unsuccessfully.
  def run_app(*command, stdin: "", **overrides)
    out, err, status = Open3.capture3(env(**overrides), *command, stdin_data: stdin, chdir: APP)
    assert status.success?, "#{command.join(" ")} failed:\n#{out}\n#{err}"
    out
  end

  # ruby_app boots the application with a plain require, as Puma, Sidekiq
  # or any long-running process does, and runs code after the boot.
  def ruby_app(code, **overrides)
    run_app("ruby", "-e", %(require "./config/environment"\n#{code}), **overrides)
  end

  # logged returns the log events the subprocesses wrote, optionally only
  # those named event.
  def logged(event = nil)
    return [] unless File.exist?(@log)

    events = File.readlines(@log).map { |line| JSON.parse(line) }
    event ? events.select { |e| e["event"] == event.to_s } : events
  end

  # summary_counts sums the evaluation summary rows the mock server
  # received for flag across every summary.
  def summary_counts(flag)
    @server.events_requests.flat_map(&:events).select { |e| e["type"] == "evaluation_summary" }
           .flat_map { |e| e["counts"] }.select { |c| c["flag"] == flag }.sum { |c| c["count"] }
  end

  def eventually(timeout = 10)
    deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
    loop do
      result = yield
      return result if result || Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline

      sleep 0.05
    end
  end
end
