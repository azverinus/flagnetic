# frozen_string_literal: true

require "rails/railtie"
require_relative "../flagnetic"
require_relative "controller_helpers"

module Flagnetic
  # Railtie builds Flagnetic.client from the Rails configuration. The gem
  # loads it only when Rails is loaded; nothing else in the gem knows Rails.
  #
  # Configuration, all under config.flagnetic, with the SDK key and base
  # URL taken from the first of config, the FLAGNETIC_SDK_KEY and
  # FLAGNETIC_BASE_URL environment variables, and credentials
  # (flagnetic.sdk_key, flagnetic.base_url):
  #
  # - sdk_key, base_url: see above. Without both the client serves
  #   built-in defaults and logs client_not_configured.
  # - offline_file: a ruleset document to serve instead; no network.
  # - start: nil (default) starts the client at boot in a server or job
  #   process and on first use in rails console, rails runner and rake;
  #   true starts it at boot everywhere; false only on first use.
  # - ready_timeout: seconds a boot waits for the ruleset (default 5).
  # - close_timeout: seconds the exit flush may take (default 5).
  # - allow_overrides: false disables local overrides, which are otherwise
  #   allowed in every Rails environment but production, where no option
  #   enables them.
  # - logger: the logger option of Client.new (default Rails.logger).
  # - client_options: any other Client.new option, as a Hash.
  # - platform, app_version: predeclared attributes of every request
  #   context, and the client's platform and app_version options, so
  #   User-Agent, evaluation summaries and exposures outside a request
  #   carry them too.
  # - user_unit_type, user_id, anonymous_unit_type, anonymous_cookie,
  #   attributes: how the controller helper builds a request's context;
  #   see ControllerHelpers.
  #
  # The client is started per process: a Puma or Unicorn worker starts its
  # own background thread the moment it is forked, and every process
  # closes its client at exit, which flushes what it has queued.
  class Railtie < ::Rails::Railtie
    config.flagnetic = ActiveSupport::OrderedOptions.new
    config.flagnetic.start = nil
    config.flagnetic.ready_timeout = 5
    config.flagnetic.close_timeout = 5
    config.flagnetic.client_options = {}
    config.flagnetic.anonymous_cookie = "flagnetic_anonymous_id"
    config.flagnetic.user_id = ControllerHelpers::SIGNED_IN_USER_ID

    # EMPTY is the ruleset an unconfigured client serves: no flags, so every
    # getter returns its built-in default.
    EMPTY = { "schema_version" => 1, "version" => 0, "scope" => "server", "flags" => [] }.freeze
    private_constant :EMPTY

    # A Rakefile loads the application's tasks before any task boots the
    # environment, so this runs first: rake is a one-off process.
    rake_tasks { Railtie.one_off = true }

    initializer "flagnetic.controller_helpers" do
      ActiveSupport.on_load(:action_controller) { include Flagnetic::ControllerHelpers }
    end

    config.after_initialize { |app| Railtie.boot(app) }

    class << self
      attr_writer :one_off

      # boot installs the builder of Flagnetic.client and, in a server or
      # job process, builds the client now and waits for its ruleset.
      def boot(app)
        config = app.config.flagnetic
        Flagnetic.client_builder = -> { build(app) }
        Process.singleton_class.prepend(ForkHook) unless Process.singleton_class < ForkHook
        start = config.start.nil? ? !one_off? : config.start
        Flagnetic.client if start
      end

      # build constructs the client from the configuration and registers
      # its close at exit.
      def build(app)
        config = app.config.flagnetic
        client = new_client(app)
        at_exit { client.close(timeout: config.close_timeout) }
        client
      end

      # after_fork starts the background thread of a forked child: a Puma
      # or Unicorn worker then follows the change stream before its first
      # request. A client not built yet is left to be built on first use.
      def after_fork
        Flagnetic.client.after_fork if Flagnetic.client?
      end

      private

      # new_client constructs the client the configuration describes.
      # Local overrides stay off in production whatever client_options say.
      def new_client(app)
        config = app.config.flagnetic
        log = Logging.adapt(config.logger || ::Rails.logger)
        sdk_key = setting(app, :sdk_key)
        base_url = setting(app, :base_url)
        allow_overrides = config.allow_overrides != false && !::Rails.env.production?
        options = { **config.client_options, logger: log, allow_overrides: allow_overrides }
        options[:platform] = config.platform if config.platform
        options[:app_version] = config.app_version if config.app_version
        if config.offline_file
          Client.new(offline_file: config.offline_file, **options)
        elsif sdk_key && base_url
          Client.new(sdk_key, base_url: base_url, **options).tap { |c| c.ready(timeout: config.ready_timeout) }
        else
          log.call(:warn, :client_not_configured, { rails_env: ::Rails.env.to_s })
          Client.new(offline: EMPTY, **options, send_summaries: false)
        end
      end

      # setting reads name from config, the environment variable, then
      # credentials; blank is unset.
      def setting(app, name)
        [app.config.flagnetic[name], ENV.fetch("FLAGNETIC_#{name.upcase}", nil), credential(app, name)].find(&:present?)
      end

      def credential(app, name)
        app.credentials.dig(:flagnetic, name)
      rescue StandardError
        nil
      end

      # one_off tells whether this process is rails console, rails runner or
      # rake: a process the client must not start in unless it is asked
      # for.
      def one_off?
        @one_off || defined?(::Rails::Console) || defined?(::Rails::Command::RunnerCommand) ? true : false
      end
    end

    # ForkHook runs Railtie.after_fork in every forked child (Ruby 3.1's
    # Process._fork, which every fork goes through).
    module ForkHook
      def _fork
        pid = super
        Railtie.after_fork if pid.zero?
        pid
      end
    end
  end
end
