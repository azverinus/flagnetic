# frozen_string_literal: true

require "time"

module Flagnetic
  # Recorder holds in memory what an offline client would report, so a
  # test can assert on it: the running evaluation counts, every evaluation
  # summary a flush or close would have sent, and every exposure and alias
  # the client recorded. It is thread-safe.
  class Recorder
    # SummaryCount is one row of an evaluation summary: how often a flag
    # served a variation for an evaluation reason. It carries no unit IDs
    # and no values; variation is nil for local_override and fallback.
    # count is the wire name; Struct#count is not used on it.
    SummaryCount = Struct.new(:flag, :variation, :reason, :count, keyword_init: true) # rubocop:disable Lint/StructNewOverride

    # EvaluationSummary is one evaluation summary an offline client would
    # have sent, as its own event: a deduplication key, the interval and
    # ruleset version it reports, the app_version option, and its rows.
    EvaluationSummary = Struct.new(:event_id, :interval_start, :interval_end, :ruleset_version, :app_version, :counts,
                                   keyword_init: true) do
      def initialize(**fields)
        super
        freeze
      end
    end

    # Exposure is one exposure an offline client would have sent.
    Exposure = Struct.new(:event_id, :experiment_key, :flag_key, :variation_key, :unit_type, :unit_id, :platform,
                          :app_version, :reason, :override, :ruleset_version, :occurred_at, keyword_init: true) do
      def initialize(**fields)
        super
        freeze
      end
    end

    # Alias is one alias an offline client would have sent.
    Alias = Struct.new(:event_id, :from_unit_type, :from_unit_id, :to_unit_type, :to_unit_id, :occurred_at,
                       keyword_init: true) do
      def initialize(**fields)
        super
        freeze
      end
    end

    def initialize
      @mutex = Mutex.new
      @counts = Hash.new(0)
      @summaries = []
      @exposures = []
      @aliases = []
    end

    # summary returns the running evaluation counts since construction or
    # the last reset, ordered by flag, variation and reason. Closing a
    # window (flush, close) does not touch it.
    def summary
      rows = @mutex.synchronize { @counts.dup }
      rows.map { |(flag, variation, reason), n| SummaryCount.new(flag:, variation:, reason:, count: n).freeze }
          .sort_by { |r| [r.flag, r.variation.to_s, r.reason.to_s] }
    end

    # reset forgets everything recorded.
    def reset
      @mutex.synchronize do
        @counts.clear
        @summaries.clear
        @exposures.clear
        @aliases.clear
      end
    end

    # count records one evaluation into the running total.
    def count(flag, detail)
      key = [flag, detail.variation, detail.reason]
      @mutex.synchronize { @counts[key] += 1 }
    end

    # record keeps an evaluation summary the client would have sent once a
    # window closed.
    def record(summary)
      @mutex.synchronize { @summaries << summary }
    end

    # evaluation_summaries returns the evaluation summaries the client
    # would have sent since construction or the last reset, in the order
    # they were reported.
    def evaluation_summaries
      @mutex.synchronize { @summaries.dup }
    end

    # record_event keeps an exposure or an alias the client would have
    # sent, from the wire-shaped Hash Exposures builds. An offline client
    # records it at the call itself, so a test asserts on it without
    # flushing.
    def record_event(event)
      case event["type"]
      when "exposure"
        @mutex.synchronize do
          @exposures << Exposure.new(event_id: event["event_id"], experiment_key: event["experiment_key"],
                                     flag_key: event["flag_key"], variation_key: event["variation_key"],
                                     unit_type: event["unit_type"], unit_id: event["unit_id"],
                                     platform: event["platform"], app_version: event["app_version"],
                                     reason: event["evaluation_reason"].to_sym, override: event["override"],
                                     ruleset_version: event["ruleset_version"],
                                     occurred_at: Time.parse(event["occurred_at"]))
        end
      when "alias"
        @mutex.synchronize do
          @aliases << Alias.new(event_id: event["event_id"], from_unit_type: event["from_unit_type"],
                                from_unit_id: event["from_unit_id"], to_unit_type: event["to_unit_type"],
                                to_unit_id: event["to_unit_id"], occurred_at: Time.parse(event["occurred_at"]))
        end
      end
    end

    # exposures returns the exposures the client would have sent since
    # construction or the last reset, in the order they were recorded.
    # Repeats per experiment, unit and variation are suppressed before they
    # reach the recorder, exactly as they are online.
    def exposures
      @mutex.synchronize { @exposures.dup }
    end

    # aliases returns the aliases the client would have sent since
    # construction or the last reset, in the order they were recorded.
    def aliases
      @mutex.synchronize { @aliases.dup }
    end

    # The client hands the recorder what it counts and reports; a test only
    # reads it.
    private :count, :record, :record_event
  end
end
