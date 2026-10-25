# frozen_string_literal: true

require_relative "test_helper"

# The railtie, observed from outside: the test application booted in a
# subprocess the ways an application boots, against the mock server.
class RailtieTest < Minitest::Test
  include AppProcess

  REPORT = <<~RUBY
    c = Flagnetic.client
    puts JSON.generate(stage: c.stage, value: c.bool("new_checkout", default: false),
                       reason: c.detail("new_checkout", default: false).reason)
  RUBY

  def test_a_process_boot_starts_the_client_from_the_environment_variables_and_waits_for_the_ruleset
    out = ruby_app(REPORT)
    assert_equal({ "stage" => "fresh", "value" => true, "reason" => "fallthrough" }, JSON.parse(out))
    assert_equal "Bearer fn_srv_test", @server.fetches.first["authorization"]
    assert_empty logged(:client_not_configured)
  end

  def test_the_client_starts_from_rails_credentials
    credentials = File.join(@dir, "credentials")
    write_credentials(credentials, "flagnetic:\n  sdk_key: fn_srv_from_credentials\n  base_url: #{@server.base_url}\n")
    out = ruby_app(REPORT, FLAGNETIC_TEST_CREDENTIALS: credentials, FLAGNETIC_SDK_KEY: nil, FLAGNETIC_BASE_URL: nil)
    assert_equal "fresh", JSON.parse(out)["stage"]
    assert_equal "Bearer fn_srv_from_credentials", @server.fetches.first["authorization"]
  end

  def test_the_environment_variables_win_over_credentials
    credentials = File.join(@dir, "credentials")
    write_credentials(credentials, "flagnetic:\n  sdk_key: fn_srv_from_credentials\n  base_url: http://127.0.0.1:9\n")
    ruby_app(REPORT, FLAGNETIC_TEST_CREDENTIALS: credentials)
    assert_equal "Bearer fn_srv_test", @server.fetches.first["authorization"]
  end

  def test_the_configuration_wins_over_the_environment_variables
    ruby_app(REPORT, FLAGNETIC_TEST_CONFIG_SDK_KEY: "fn_srv_from_config")
    assert_equal "Bearer fn_srv_from_config", @server.fetches.first["authorization"]
  end

  def test_without_a_key_the_client_serves_built_in_defaults_and_says_so
    out = ruby_app(REPORT, FLAGNETIC_SDK_KEY: nil, FLAGNETIC_BASE_URL: nil)
    assert_equal({ "stage" => "fresh", "value" => false, "reason" => "fallback" }, JSON.parse(out))
    assert_empty @server.requests
    assert_equal([{ "rails_env" => "development" }],
                 logged(:client_not_configured).map { |e| e.slice("rails_env") })
  end

  def test_local_overrides_are_allowed_outside_production
    out = ruby_app(<<~RUBY)
      Flagnetic.client.local_overrides.set("new_checkout", false)
      puts Flagnetic.client.detail("new_checkout", default: true).reason
    RUBY
    assert_equal "local_override", out.chomp
  end

  def test_local_overrides_are_refused_in_production
    out = ruby_app(<<~RUBY, RAILS_ENV: "production")
      begin
        Flagnetic.client.local_overrides.set("new_checkout", false)
      rescue Flagnetic::LocalOverridesNotAllowed
        puts "refused"
      end
    RUBY
    assert_equal "refused", out.chomp
  end

  def test_no_client_option_enables_local_overrides_in_production
    out = ruby_app(<<~RUBY, RAILS_ENV: "production", FLAGNETIC_TEST_CLIENT_ALLOWS_OVERRIDES: "1")
      begin
        Flagnetic.client.local_overrides.set("new_checkout", false)
      rescue Flagnetic::LocalOverridesNotAllowed
        puts "refused"
      end
    RUBY
    assert_equal "refused", out.chomp
    assert_equal(["not_allowed"], logged(:override_refused).map { |e| e["reason"] })
  end

  def test_close_on_exit_flushes_the_evaluation_summary
    ruby_app(%(3.times { Flagnetic.client.bool("new_checkout", default: false) }))
    assert_equal 3, summary_counts("new_checkout")
  end

  def test_the_configured_platform_and_app_version_reach_the_client
    ruby_app(%(Flagnetic.client.bool("new_checkout", default: false)))
    summaries = @server.events_requests.flat_map(&:events).select { |e| e["type"] == "evaluation_summary" }
    assert_equal ["2.3.0"], summaries.map { |e| e["app_version"] }.uniq
    assert_match(/web/, @server.fetches.first["user-agent"])
  end

  def test_rails_console_starts_nothing_until_the_client_is_asked_for
    out = run_app("bin/rails", "console", stdin: <<~RUBY)
      puts "before=\#{Flagnetic.client?}"
      puts "value=\#{Flagnetic.client.bool("new_checkout", default: false)} stage=\#{Flagnetic.client.stage}"
      exit
    RUBY
    assert_includes out, "before=false"
    assert_includes out, "value=true stage=fresh"
    assert_equal 1, @server.fetches.size, "one client, built on first use"
  end

  def test_rails_console_makes_no_request_when_no_flag_is_asked_for
    run_app("bin/rails", "console", stdin: "puts 1 + 1\nexit\n")
    assert_empty @server.requests
  end

  def test_rails_runner_starts_nothing_unless_asked
    out = run_app("bin/rails", "runner", "puts \"client_started=\#{Flagnetic.client?}\"")
    assert_includes out, "client_started=false"
    assert_empty @server.requests
  end

  def test_rake_tasks_start_nothing_unless_asked
    out = run_app("bundle", "exec", "rake", "report")
    assert_includes out, "client_started=false"
    assert_empty @server.requests
  end

  def test_config_start_forces_the_client_to_start_in_a_one_off_process
    out = run_app("bin/rails", "runner", "puts \"client_started=\#{Flagnetic.client?}\"",
                  FLAGNETIC_TEST_START: "true")
    assert_includes out, "client_started=true"
    refute_empty @server.fetches
  end

  private

  # write_credentials encrypts yaml the way `rails credentials:edit` does,
  # at path.yml.enc with its key at path.key.
  def write_credentials(path, yaml)
    run_app("ruby", "-e", <<~RUBY)
      require "bundler/setup"
      require "active_support"
      require "active_support/encrypted_configuration"
      File.write("#{path}.key", ActiveSupport::EncryptedConfiguration.generate_key)
      ActiveSupport::EncryptedConfiguration.new(config_path: "#{path}.yml.enc", key_path: "#{path}.key",
                                                env_key: "RAILS_MASTER_KEY", raise_if_missing_key: true)
                                           .write(#{yaml.inspect})
    RUBY
  end
end
