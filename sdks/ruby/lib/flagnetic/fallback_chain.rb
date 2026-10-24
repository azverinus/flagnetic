# frozen_string_literal: true

require "json"

module Flagnetic
  # FallbackChain is the part of Client that holds the ruleset: it loads
  # the store and offline documents, applies each fetch outcome and moves
  # the stage. Every method is private; the background thread calls handle_fetch,
  # held_version and key_revoked.
  module FallbackChain
    # Loaded is the held ruleset and when it was received.
    Loaded = Struct.new(:ruleset, :received_at, keyword_init: true)

    private

    def held_version = @loaded&.ruleset&.version

    def load_offline
      ruleset = decode(@offline_source.call)
      unless ruleset.is_a?(Eval::Ruleset)
        cause = ruleset.is_a?(Array) ? "schema_version #{ruleset[1]} is unsupported" : ruleset
        raise OfflineError, "offline ruleset: #{cause}"
      end

      swap(ruleset, received_at: Time.now, stage: :fresh, source: "offline")
    rescue SystemCallError, IOError, TypeError => e
      raise OfflineError, "offline ruleset: #{e.message}"
    end

    # load_store serves a persisted ruleset as last_good before any network
    # call. A corrupt file leaves the stage built_in, with a log.
    def load_store
      json, received_at = @store.load
      return unless json

      ruleset = decode(json)
      unless ruleset.is_a?(Eval::Ruleset)
        cause = ruleset.is_a?(Array) ? "schema_version #{ruleset[1]} is unsupported" : ruleset
        return log(:error, :store_failed, path: @store.to_s, cause: cause)
      end

      swap(ruleset, received_at: received_at, stage: :last_good, source: "store")
    rescue SystemCallError, IOError => e
      log(:error, :store_failed, path: @store.to_s, cause: "#{e.class}: #{e.message}")
    end

    # decode returns the ruleset a fetched or stored document holds, or the
    # reason it is refused: it must parse, carry a supported schema_version
    # and be of the server scope.
    def decode(json)
      doc = JSON.parse(json)
      return "parse: the document is not a JSON object" unless doc.is_a?(Hash)

      schema = doc["schema_version"]
      return "parse: schema_version is missing" unless schema.is_a?(Integer)
      return [:schema, schema] unless schema.between?(SCHEMA_VERSION_MIN, SCHEMA_VERSION_MAX)
      return "parse: scope is #{doc["scope"].inspect}, want \"server\"" unless doc["scope"] == "server"

      Eval::Ruleset.decode(json)
    rescue JSON::ParserError, EncodingError => e
      "parse: #{e.message.lines.first&.strip}"
    rescue Eval::DecodeError => e
      "parse: #{e.message}"
    end

    # swap installs a ruleset, moves to stage and returns the flags whose
    # evaluation may have changed, local overrides dropped by a protected
    # environment included.
    def swap(ruleset, received_at:, stage:, source:)
      # The ruleset and its stage change together, so no getter sees one
      # without the other.
      previous, previous_stage = @mutex.synchronize do
        p = [@loaded, @stage]
        @loaded = Loaded.new(ruleset: ruleset, received_at: received_at).freeze
        @stage = stage
        p
      end
      log(:info, :ruleset_loaded, source: source, ruleset_version: ruleset.version, flags: ruleset.flags.size)
      report_unsupported(ruleset)
      stage_changed(previous_stage, stage)
      (Changes.flags(previous&.ruleset, ruleset) | refuse_local_overrides(ruleset)).sort
    end

    # report_unsupported logs rule_unsupported for every flag of ruleset
    # holding a construct this evaluator does not know, once per flag and
    # ruleset version: the evaluator finds them when it decodes, so a flag
    # nobody evaluates is reported too, and an SDK that must be upgraded
    # says so without a line per evaluation.
    def report_unsupported(ruleset)
      ruleset.unsupported_rules.sort.each do |key, constructs|
        next unless first_report?(key, ruleset.version)

        log(:error, :rule_unsupported, flag: key, constructs: constructs.map { |c| "#{c.kind} #{c.name}" },
                                       ruleset_version: ruleset.version)
      end
    end

    def first_report?(key, version)
      @mutex.synchronize do
        next false if @unsupported_logged[key] == version

        @unsupported_logged[key] = version
        true
      end
    end

    def move_to(stage)
      previous = @mutex.synchronize do
        p = @stage
        @stage = stage
        p
      end
      stage_changed(previous, stage)
    end

    def stage_changed(previous, stage)
      return if previous == stage

      log(stage == :fresh ? :info : :warn, :stage_changed, from: previous, to: stage)
      fire(@stage_listeners, stage)
    end

    # handle_fetch applies a fetch outcome (a Transport::Response or the
    # exception raised) to the fallback chain and returns :ok, :failed or
    # :revoked. Called by the background thread.
    def handle_fetch(response)
      outcome =
        if response.is_a?(Exception) then fetch_failed("#{response.class}: #{response.message}")
        else
          case response.status
          when 200 then accept(response.body)
          when 304 then not_modified
          when 401, 403 then key_revoked(response.status, "fetch")
          else fetch_failed("HTTP #{response.status}")
          end
        end
      mark_ready
      outcome
    end

    def accept(json)
      ruleset = decode(json)
      return schema_unsupported(ruleset[1]) if ruleset.is_a?(Array)
      return fetch_failed(ruleset) if ruleset.is_a?(String)

      held = held_version
      return fetch_failed("version #{ruleset.version} is below the held #{held}") if held && ruleset.version < held

      changed = swap(ruleset, received_at: Time.now, stage: :fresh, source: "fetch")
      succeeded
      persist(json)
      fire_change(changed)
      :ok
    end

    def not_modified
      return fetch_failed("304 without a held ruleset") unless @loaded

      move_to(:fresh)
      succeeded
      :ok
    end

    def fetch_failed(cause)
      log(:warn, :fetch_failed, cause: cause, stage: @stage)
      failed
      :failed
    end

    # failed moves fresh to last_good and marks the held ruleset stale;
    # last_good and built_in stay.
    def failed
      @mutex.synchronize { @stale_since ||= Time.now }
      move_to(:last_good) if @stage == :fresh
    end

    def succeeded
      @mutex.synchronize do
        @stale_since = nil
        @once.delete([:key_revoked, "fetch"])
      end
    end

    def schema_unsupported(schema)
      once(:schema_unsupported) do
        log(:error, :schema_unsupported, schema_version: schema,
                                         supported: (SCHEMA_VERSION_MIN..SCHEMA_VERSION_MAX).to_a)
      end
      fetch_failed("schema_version #{schema} is unsupported")
    end

    # key_revoked keeps serving: the stage leaves fresh as for any failure,
    # key_revoked is logged once at error per source, until a fetch
    # succeeds again or the stream connects again. A refused fetch is a
    # failure of the fallback chain; a refused stream only closes it.
    def key_revoked(status, source)
      once([:key_revoked, source]) { log(:error, :key_revoked, status: status, source: source) }
      failed if source == "fetch"
      :revoked
    end

    def stream_connected
      @mutex.synchronize { @once.delete([:key_revoked, "stream"]) }
      log(:info, :sse_connected)
    end

    def persist(json)
      @store&.save(json)
    rescue SystemCallError, IOError => e
      log(:error, :store_failed, path: @store.to_s, cause: "#{e.class}: #{e.message}")
    end

    def mark_ready
      @ready_mutex.synchronize do
        @ready = true
        @ready_cond.broadcast
      end
    end
  end
  private_constant :FallbackChain
end
