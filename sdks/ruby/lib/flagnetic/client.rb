# frozen_string_literal: true

require "json"

module Flagnetic
  # Client evaluates flags in server mode: it holds the environment's
  # ruleset in memory and evaluates locally. Construct one per process and
  # share it across threads; it survives fork.
  #
  #   client = Flagnetic::Client.new(ENV.fetch("FLAGNETIC_SDK_KEY"), base_url: "https://flags.example.com")
  #   client.ready(timeout: 5) # => :fresh, :last_good or :built_in
  #   client.bool("new_checkout", default: false, context: { units: { user: "u-1001" } })
  #
  # Threads: the ruleset is a frozen object swapped under a mutex and read
  # without locking, so getters never wait. One background thread runs the
  # change stream, polling and retries.
  #
  # Fork: the first call in a forked child (Puma cluster mode, Unicorn)
  # sees the new process ID and starts the child's own background thread;
  # the child keeps serving the ruleset it inherited meanwhile. after_fork
  # starts it at once, from a fork hook.
  #
  # Getters never raise: any failure serves the built-in default with
  # reason fallback and a log event.
  class Client
    include FallbackChain

    # Timing holds the delays and intervals of the background thread, in
    # seconds. Only poll_interval and request_timeout are public options;
    # the timing option exists for tests and is not part of the API.
    Timing = Struct.new(:request_timeout, :fetch_base, :fetch_cap, :unauthorized_cap, :stream_base, :stream_cap,
                        :stream_stable, :close_jitter, :silence, :poll_connected, :poll_down, :event_retries,
                        :exposure_flush, :summary_flush, keyword_init: true)
    DEFAULT_TIMING = Timing.new(
      request_timeout: 10, fetch_base: 1, fetch_cap: 300, unauthorized_cap: 900, stream_base: 1, stream_cap: 30,
      stream_stable: 60, close_jitter: 2, silence: 45, poll_connected: 300, poll_down: 30, event_retries: [1, 2, 4],
      exposure_flush: 2, summary_flush: 60
    ).freeze
    MIN_POLL_INTERVAL = 30
    TYPES = %w[boolean string number json].freeze
    private_constant :Timing, :DEFAULT_TIMING, :TYPES

    # new constructs a client for a server key and the base URL of the
    # instance. It fails loudly (ArgumentError) only for a client key and
    # for arguments that cannot work (no base_url); never for network
    # reasons. Options:
    #
    # - offline: a ruleset document (JSON text or a Hash) exactly as the
    #   ruleset endpoint serves it, or a callable returning one on every
    #   reload; offline_file: a path to one. No network
    #   call is ever made; the stage is fresh; reload re-reads the source.
    # - store: a file path holding the last good ruleset, loaded as
    #   last_good at construction and rewritten on every accepted ruleset.
    # - logger: a callable receiving (level, event, fields), or a Logger.
    #   Default: warn and above to $stderr.
    # - stream: false disables the change stream; polling then runs every
    #   30 s.
    # - poll_interval: seconds between safety polls while the stream is
    #   connected (default 300, at least 30).
    # - request_timeout: seconds per request (default 10).
    # - allow_overrides: enables local_overrides (logged as a warning).
    # - platform: sent in User-Agent and the events envelope, and an
    #   exposure's platform where the context names none (default
    #   "server").
    # - app_version: the application's version, sent with every evaluation
    #   summary and as an exposure's app_version where the context names
    #   none.
    # - flush_interval: seconds between flushes of the exposure and alias
    #   queue (default 2); also how long one batch may be retried before
    #   its events go back into the queue, and how long a repeat per
    #   experiment, unit and variation is suppressed for.
    # - summary_interval: seconds between evaluation summary reports
    #   (default 60); also how long one report may be retried before its
    #   window merges into the next.
    # - send_summaries: false stops evaluation summaries from being counted
    #   and sent (default true).
    # - send_exposures: false stops exposure and alias from sending
    #   anything (default true; logged as a warning at construction).
    def initialize(sdk_key = nil, base_url: nil, offline: nil, offline_file: nil, store: nil, logger: nil,
                   stream: true, poll_interval: 300, request_timeout: 10, allow_overrides: false,
                   platform: "server", app_version: nil, flush_interval: 2, summary_interval: 60,
                   send_summaries: true, send_exposures: true, timing: nil)
      if sdk_key.to_s.start_with?("fn_cli_")
        raise ArgumentError, "Flagnetic::Client is server mode and needs a server key (fn_srv_...); " \
                             "a client key (fn_cli_...) belongs to a client-mode SDK"
      end

      @log = Logging.adapt(logger)
      @allow_overrides = allow_overrides
      @send_summaries = send_summaries != false
      @send_exposures = send_exposures != false
      @platform = platform
      @app_version = app_version&.to_s
      init_state
      log(:warn, :local_overrides_allowed) if allow_overrides
      log(:warn, :exposures_disabled) unless @send_exposures
      @timing = build_timing(poll_interval, request_timeout, flush_interval, summary_interval, timing)

      if offline || offline_file
        start_offline(offline, offline_file)
      else
        raise ArgumentError, "base_url is required unless the client is offline" if base_url.nil?

        start_online(sdk_key, base_url, store: store, stream: stream, platform: platform)
      end
    end

    # bool serves a boolean flag for context, or default with reason
    # fallback.
    def bool(key, default:, context: nil) = serve(key, "boolean", default, context).value

    # string serves a string flag for context, or default.
    def string(key, default:, context: nil) = serve(key, "string", default, context).value

    # number serves a number flag (an Integer or Float, as the ruleset has
    # it) for context, or default.
    def number(key, default:, context: nil) = serve(key, "number", default, context).value

    # int serves a number flag as an Integer for context, or default. A
    # served number that is not whole serves default with error
    # type_mismatch rather than a truncated value.
    def int(key, default:, context: nil) = serve(key, "number", default, context, int: true).value

    # json serves a JSON flag's value, parsed and frozen, for context, or
    # default.
    def json(key, default:, context: nil) = serve(key, "json", default, context).value

    # detail serves a flag with why it was served. default's class names the
    # flag type asked for: true or false is boolean, a String is string, a
    # Numeric is number, anything else JSON.
    def detail(key, default:, context: nil) = serve(key, Client.__send__(:type_of, default), default, context)

    # stage returns the current stage of the fallback chain: :built_in,
    # :last_good or :fresh.
    def stage
      ensure_worker
      @stage
    end

    # status returns the stage and since when the held ruleset is stale.
    def status
      ensure_worker
      @mutex.synchronize { Status.new(stage: @stage, stale_since: @stale_since) }
    end

    # ready waits until the first fetch has an outcome, or timeout seconds,
    # and returns the stage reached. It never raises. Evaluating before
    # ready is legal and follows the fallback chain.
    def ready(timeout: 5)
      ensure_worker
      deadline = now + timeout
      @ready_mutex.synchronize do
        until @ready
          left = deadline - now
          break unless left.positive?

          @ready_cond.wait(@ready_mutex, left)
        end
      end
      @stage
    end

    # on_change registers a block called with the sorted keys of the flags
    # whose value may have changed: after each accepted ruleset (through a
    # segment, a declared attribute or unit type or a prerequisite as well
    # as their own configuration) and on every local override change. It
    # runs on the thread that caused the change; an exception is logged as
    # listener_failed. Returns a callable that unregisters the block.
    def on_change(&block) = listen(@change_listeners, block)

    # on_stage registers a block called with the new stage on every
    # transition. Returns a callable that unregisters it.
    def on_stage(&block) = listen(@stage_listeners, block)

    # recorder is the in-memory Recorder of an offline client, holding what
    # it would have sent; nil online.
    attr_reader :recorder

    # local_overrides manages substitutions of flag values inside this
    # process; see LocalOverrides.
    def local_overrides = LocalOverrides.new(self)

    # reload re-reads an offline client's document and swaps it in, firing
    # on_change with the flags whose evaluation may have changed. It raises
    # OfflineError when the client is not offline or the document fails;
    # the held ruleset then stays.
    def reload
      raise OfflineError, "reload needs an offline client" unless @offline_source

      changed = @reload_mutex.synchronize { load_offline }
      fire_change(changed)
      true
    end

    # exposure records that the unit described by context encountered the
    # behaviour flag key serves: it looks up the most recent detail of key
    # for context and sends an exposure only when it carries an experiment
    # block; otherwise nothing is sent and exposure_skipped names why, at
    # debug. A local override or a flag never evaluated with an experiment
    # sends nothing. A repeat for the same experiment, unit and variation is
    # suppressed for at least flush_interval seconds; the platform's own
    # deduplication by event ID is what analysis relies on. It never raises.
    def exposure(key, context: nil)
      ensure_worker
      @exposures&.exposure(context, key.to_s)
      nil
    rescue StandardError => e
      log(:error, :evaluation_failed, flag: key.to_s, cause: "#{e.class}: #{e.message}")
      nil
    end

    # alias records that two unit IDs belong to the same person, typically
    # an anonymous ID and a user ID after sign-up. It changes no context:
    # an experiment spanning sign-up keeps hashing on the unit type it
    # declared, and the alias is what lets analysis join the two. It never
    # raises.
    def alias(from_unit_type, from_unit_id, to_unit_type, to_unit_id)
      ensure_worker
      @exposures&.add_alias(from_unit_type, from_unit_id, to_unit_type, to_unit_id)
      nil
    rescue StandardError => e
      log(:error, :evaluation_failed, flag: "alias", cause: "#{e.class}: #{e.message}")
      nil
    end

    # flush sends what is queued of exposures and aliases, then closes the
    # window of evaluation counts and sends it as one or more evaluation
    # summaries, waiting at most timeout seconds for the platform's answer.
    # Nothing queued and nothing evaluated is nothing sent. An offline
    # client hands both to its Recorder instead, at once. It never raises;
    # events that fail stay queued and a window that fails merges into the
    # next one.
    def flush(timeout: 5)
      do_flush(timeout)
      true
    end

    # close flushes the queued exposures and aliases and the current window
    # of evaluation counts, then stops the stream and polling, all within
    # timeout seconds: what the flush leaves of the timeout is what the
    # background thread gets to end before it is killed. Getters keep
    # serving the held ruleset.
    def close(timeout: 5)
      started = now
      do_flush(timeout) unless @closed
      worker = @fork_mutex.synchronize do
        @closed = true
        w = @worker
        @worker = nil
        w
      end
      if worker && @pid == Process.pid
        worker.stop([timeout - (now - started), 0].max)
      else
        worker&.abandon
      end
      nil
    end

    # after_fork starts a forked child's background thread now rather than
    # on the child's first call, so a worker that has not served a request
    # yet already follows the change stream. It does nothing in the process
    # that built the client and never raises. The Rails integration calls
    # it from a fork hook.
    def after_fork
      ensure_worker unless @closed
      nil
    end

    # type_of names the flag type of a Ruby value.
    def self.type_of(value)
      case value
      when true, false then "boolean"
      when String then "string"
      when Numeric then "number"
      else "json"
      end
    end
    private_class_method :type_of

    private

    def now = Process.clock_gettime(Process::CLOCK_MONOTONIC)

    def init_state
      @mutex = Mutex.new
      @fork_mutex = Mutex.new
      @reload_mutex = Mutex.new
      @listeners_mutex = Mutex.new
      @change_listeners = {}
      @stage_listeners = {}
      @next_listener = 0
      @ready_mutex = Mutex.new
      @ready_cond = ConditionVariable.new
      @ready = false
      @loaded = nil
      @stage = :built_in
      @stale_since = nil
      @overrides = {}.freeze
      @once = {}
      @unsupported_logged = {}
      @pid = Process.pid
      @closed = false
    end

    def build_timing(poll_interval, request_timeout, flush_interval, summary_interval, overrides)
      Timing.new(**DEFAULT_TIMING.to_h,
                 poll_connected: [poll_interval, MIN_POLL_INTERVAL].max,
                 request_timeout: request_timeout,
                 exposure_flush: flush_interval,
                 summary_flush: summary_interval,
                 **overrides.to_h).freeze
    end

    def start_offline(document, path)
      document = JSON.generate(document, max_nesting: false) if document.is_a?(Hash)
      @offline_source =
        if path then -> { File.binread(path) }
        elsif document.respond_to?(:call) then document
        else
          -> { document }
        end
      @recorder = Recorder.new
      build_reporters(nil, @recorder)
      begin
        load_offline
      rescue OfflineError => e
        log(:error, :fetch_failed, source: "offline", cause: e.message)
      end
      mark_ready
    end

    def start_online(sdk_key, base_url, store:, stream:, platform:)
      @stream_enabled = stream
      @transport = Transport.new(sdk_key: sdk_key, base_url: base_url, platform: platform,
                                 timeout: @timing.request_timeout)
      build_reporters(@transport, nil)
      @store = store && FileStore.new(store)
      load_store if @store
      @worker = Worker.new(self, @transport, @timing, stream: stream)
    end

    # build_reporters constructs the evaluation summary counter and the
    # exposure and alias queue, unless send_summaries or send_exposures
    # turned either off, in which case counting and every call are no-ops.
    # Online they post through transport; offline they hand what they
    # would have sent to recorder at once.
    def build_reporters(transport, recorder)
      if @send_summaries
        @summaries = Summaries.new(transport: transport, timing: @timing, log: method(:log_hook),
                                   record: recorder&.method(:record), app_version: @app_version,
                                   ruleset_version: -> { @loaded&.ruleset&.version })
      end
      return unless @send_exposures

      @exposures = Exposures.new(transport: transport, timing: @timing, log: method(:log_hook),
                                 record: recorder&.method(:record_event), platform: @platform,
                                 app_version: @app_version, wake: -> { @worker&.wake })
    end

    # log_hook adapts Client#log's keyword arguments to the (level, event,
    # fields) callable Exposures and Summaries expect.
    def log_hook(level, event, fields) = log(level, event, **fields)

    # exposures and summaries are read by Worker to drive their periodic
    # flush; nil when either is off.
    attr_reader :exposures, :summaries

    # ensure_worker restarts the background thread in a forked child: the
    # thread that ran in the parent does not exist there. The child's
    # exposure and alias queue and evaluation summary window start empty;
    # the parent, which never reaches restart_worker, keeps its own.
    def ensure_worker
      return if @pid == Process.pid

      restart_worker
    rescue StandardError => e
      log(:error, :worker_failed, cause: "#{e.class}: #{e.message}")
    end

    def restart_worker
      @fork_mutex.synchronize do
        return if @pid == Process.pid

        if @worker
          @worker.abandon
          @worker = Worker.new(self, @transport, @timing, stream: @stream_enabled)
        end
        @exposures&.reset_after_fork
        @summaries&.reset_after_fork
        @pid = Process.pid
      end
    end

    # do_flush sends the exposure and alias queue and the evaluation
    # summary window at once, each on its own thread bounded by timeout,
    # so one deadline covers both rather than a slow queue eating the
    # window's share of it; both threads are always joined, whichever
    # raises or times out first.
    def do_flush(timeout)
      ensure_worker
      started = now
      threads = [@exposures, @summaries].compact.map do |reporter|
        Thread.new do
          reporter.flush_once(timeout)
        rescue StandardError => e
          log(:error, :worker_failed, cause: "#{e.class}: #{e.message}")
        end
      end
      threads.each { |t| t.join([timeout - (now - started), 0].max) }
    rescue StandardError => e
      log(:error, :worker_failed, cause: "#{e.class}: #{e.message}")
    end

    # --- evaluation -------------------------------------------------------

    def serve(key, type, default, context, int: false)
      ensure_worker
      key = key.to_s
      loaded = @loaded
      detail, automatic = begin
        served = evaluate(loaded, key, type, default, context)
        int ? integral(loaded, key, default, *served) : served
      rescue StandardError => e
        log(:error, :evaluation_failed, flag: key, cause: "#{e.class}: #{e.message}")
        [fallback(loaded, default, :general), false]
      end
      report(key, detail, automatic, context)
      detail
    rescue StandardError
      Detail.new(value: default, reason: :fallback, error_code: :general, stage: @stage)
    end

    # report counts a served detail for the evaluation summary (and an
    # offline client's recorder) and hands it to the exposure queue, which
    # remembers an experiment block for a later exposure call and records
    # the exposure at once for a flag with automatic exposure. A failure
    # here never changes what was served.
    def report(key, detail, automatic, context)
      @recorder&.__send__(:count, key, detail)
      @summaries&.count(key, detail)
      @exposures&.evaluated(context, key, detail, automatic)
    rescue StandardError => e
      log(:error, :evaluation_failed, flag: key, cause: "#{e.class}: #{e.message}")
    end

    # evaluate returns the served detail and whether the flag opts into
    # automatic exposure: only meaningful together with an experiment
    # block, which every early return here lacks.
    def evaluate(loaded, key, type, default, context)
      if (override = local_override(loaded, key))
        return [mismatch(loaded, key, override[0], type, default), false] if override[0] != type

        return [base(loaded, value: override[1], reason: :local_override), false]
      end
      return [fallback(nil, default, :no_ruleset), false] unless loaded

      flag = loaded.ruleset.flag(key)
      return [fallback(loaded, default, :flag_not_found), false] unless flag && TYPES.include?(flag.type)
      return [mismatch(loaded, key, flag.type, type, default), false] if flag.type != type

      result = loaded.ruleset.evaluate(key, context)
      return [fallback(loaded, default, result.error_code), false] if result.reason == :error
      return [fallback(loaded, default, :inconsistent_ruleset), false] unless value_of?(type, result.value)

      detail = base(loaded, value: result.value, reason: result.reason, variation: result.variation,
                            rule_id: result.rule_id, overridden: result.override_influenced,
                            experiment: result.experiment)
      [detail, flag.automatic_exposure]
    end

    def base(loaded, **fields)
      Detail.new(ruleset_version: loaded&.ruleset&.version, received_at: loaded&.received_at, stage: @stage, **fields)
    end

    def fallback(loaded, default, code)
      base(loaded, value: default, reason: :fallback, error_code: code)
    end

    def mismatch(loaded, key, flag_type, requested, default)
      once([:type_mismatch, key]) do
        log(:warn, :type_mismatch, flag: key, flag_type: flag_type, requested_type: requested)
      end
      fallback(loaded, default, :type_mismatch)
    end

    # integral serves a number for the int getter as an Integer; one that is
    # not whole is a type mismatch rather than a truncated value, and is
    # counted as the fallback it serves.
    def integral(loaded, key, default, detail, automatic)
      return [detail, automatic] if detail.reason == :fallback

      value = whole(detail.value)
      return [mismatch(loaded, key, "number", "int", default), false] unless value

      [Detail.new(**detail.to_h, value: value), automatic]
    end

    # whole is a served number as an Integer, or nil when it is not whole.
    def whole(value)
      return value if value.is_a?(Integer)
      return unless value.is_a?(Float) && value.finite? && value.to_r.denominator == 1

      value.to_i
    end

    def value_of?(type, value)
      case type
      when "boolean" then [true, false].include?(value)
      when "string" then value.is_a?(String)
      when "number" then value.is_a?(Numeric)
      else true
      end
    end

    def once(key)
      first = @mutex.synchronize { @once.key?(key) ? false : (@once[key] = true) }
      yield if first
    end

    # --- local overrides --------------------------------------------------

    def local_override(loaded, key)
      overrides = @overrides
      return if overrides.empty? || loaded&.ruleset&.protected?

      overrides[key]
    end

    def set_local_override(key, value)
      unless @allow_overrides
        log(:warn, :override_refused, flag: key, reason: "not_allowed")
        raise LocalOverridesNotAllowed, "local overrides need allow_overrides: true"
      end

      ensure_worker
      loaded = @loaded
      if loaded&.ruleset&.protected?
        log(:warn, :override_refused, flag: key, reason: "protected", environment: loaded.ruleset.environment.key)
        raise LocalOverrideRefused, "local override of #{key.inspect} refused: the environment " \
                                    "#{loaded.ruleset.environment.key.inspect} is protected"
      end
      flag = loaded&.ruleset&.flag(key)
      type = flag ? flag.type : Client.__send__(:type_of, value)
      normalized = LocalOverrides.__send__(:normalize, type, value)
      @mutex.synchronize { @overrides = @overrides.merge(key => [type, normalized].freeze).freeze }
      log(:warn, :override_set, flag: key)
      fire_change([key])
    end

    def remove_local_overrides(keys)
      removed = @mutex.synchronize do
        gone = keys.nil? ? @overrides.keys : keys & @overrides.keys
        @overrides = @overrides.except(*gone).freeze
        gone
      end
      fire_change(removed.sort)
    end

    def local_override_values = @overrides.transform_values { |(_, value)| value }

    def refuse_local_overrides(ruleset)
      return [] unless ruleset.protected?

      dropped = @mutex.synchronize do
        keys = @overrides.keys
        @overrides = {}.freeze
        keys
      end
      dropped.each do |k|
        log(:warn, :override_refused, flag: k, reason: "protected", environment: ruleset.environment.key)
      end
      dropped
    end

    # --- listeners and logging --------------------------------------------

    def listen(listeners, block)
      raise ArgumentError, "a block is required" unless block

      id = @listeners_mutex.synchronize do
        @next_listener += 1
        listeners[@next_listener] = block
        @next_listener
      end
      -> { @listeners_mutex.synchronize { listeners.delete(id) } && nil }
    end

    def fire_change(keys)
      fire(@change_listeners, keys.dup.freeze) unless keys.empty?
    end

    def fire(listeners, argument)
      @listeners_mutex.synchronize { listeners.values }.each do |listener|
        listener.call(argument)
      rescue StandardError => e
        log(:error, :listener_failed, cause: "#{e.class}: #{e.message}")
      end
    end

    # log calls the logger; a logger that raises is ignored, so that no
    # public method raises because of it.
    def log(level, event, **fields)
      @log.call(level, event, fields)
    rescue StandardError
      nil
    end
  end
end
