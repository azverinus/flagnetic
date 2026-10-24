# frozen_string_literal: true

require "json"
require_relative "../flagnetic"

module Flagnetic
  # Testing builds offline clients for deterministic tests: from a ruleset
  # document, a file, or a Hash of flag key to value. No client it builds
  # ever touches the network; the recorder holds what it would report.
  #
  #   require "flagnetic/testing"
  #
  #   client = Flagnetic::Testing.offline(values: { "new_checkout" => true })
  #   client.set("new_checkout", false) # now served to everyone as fallthrough
  #   client.recorder.summary           # evaluation counts
  #
  # Helpers gives RSpec examples and Minitest tests with_flags and
  # with_ruleset, which swap such a client in as Flagnetic.client for a
  # block.
  module Testing
    # VARIATION names the one variation set and values: give a flag.
    VARIATION = "value"

    # offline builds an OfflineClient from a ruleset document (JSON text or
    # a Hash, exactly as the ruleset endpoint serves it), from file:, or
    # from values:, a Hash of flag key to value where the value's class
    # decides the flag type. Other options go to Client.new. It raises
    # OfflineError when the document does not load.
    def self.offline(document = nil, file: nil, values: nil, **)
      given = [document, file, values].compact.size
      raise ArgumentError, "pass one of a document, file: or values:" unless given == 1

      source =
        if file then -> { File.binread(file) }
        elsif values then document_from(values).then { |doc| -> { doc } }
        else
          (document.is_a?(Hash) ? JSON.generate(document) : document.to_s).then { |doc| -> { doc } }
        end
      OfflineClient.new(source, **)
    end

    # document_from builds a ruleset document serving each value as the
    # fallthrough of a flag with one variation.
    def self.document_from(values)
      doc = { "schema_version" => 1, "version" => 1, "scope" => "server", "flags" => [] }
      values.each { |key, value| set_flag(doc, key.to_s, value) }
      JSON.generate(doc)
    end

    # set_flag replaces or adds the flag key in a parsed ruleset document.
    # An existing flag keeps its type and value must be of it.
    def self.set_flag(doc, key, value)
      flags = (doc["flags"] ||= [])
      at = flags.index { |f| f.is_a?(Hash) && f["key"] == key }
      type = Client.type_of(value)
      if at && (existing = flags[at]["type"]) != "json" && existing != type
        raise ArgumentError, "flag #{key.inspect} is #{existing}, a #{value.class} is #{type}"
      end

      type = flags[at]["type"] if at
      flag = { "key" => key, "type" => type, "on" => true, "variations" => [{ "key" => VARIATION, "value" => value }],
               "off_variation" => VARIATION, "fallthrough" => VARIATION }
      at ? flags[at] = flag : flags << flag
    end

    # with_flags makes an offline client serving the given flag values
    # Flagnetic.client for the block, as offline(values:) builds it, and
    # restores the previous one afterwards, even when the block raises; a
    # client the Rails integration had not built yet stays unbuilt. Keys
    # may be Strings or Symbols. It yields the client, whose recorder holds
    # what the code under test reported. The swap is process-wide: tests
    # running in parallel threads must not use it.
    #
    #   Flagnetic::Testing.with_flags("new_checkout" => true) do |client|
    #     get "/checkout"
    #     assert client.recorder.summary.any? { |row| row.flag == "new_checkout" }
    #   end
    def self.with_flags(values = {}, **more, &)
      swap(offline(values: values.merge(more)), &)
    end

    # with_ruleset is with_flags for a ruleset document (JSON text or a
    # Hash, exactly as the ruleset endpoint serves it) or file:, for a test
    # needing rules, rollouts or an experiment: the recorder then holds the
    # exposures the code under test reported.
    def self.with_ruleset(document = nil, file: nil, &)
      raise ArgumentError, "pass a document or file:" if document.nil? == file.nil?

      swap(offline(document, file: file), &)
    end

    def self.swap(client)
      raise ArgumentError, "a block is required" unless block_given?

      previous = Flagnetic.__send__(:replace_client, client)
      begin
        yield client
      ensure
        Flagnetic.__send__(:replace_client, previous.first)
        client.close(timeout: 0)
      end
    end
    private_class_method :swap

    # Helpers is with_flags and with_ruleset for test classes:
    #
    #   RSpec.configure { |config| config.include Flagnetic::Testing::Helpers }
    #
    #   class ActiveSupport::TestCase
    #     include Flagnetic::Testing::Helpers
    #   end
    module Helpers
      # with_flags is Flagnetic::Testing.with_flags.
      def with_flags(...) = Testing.with_flags(...)

      # with_ruleset is Flagnetic::Testing.with_ruleset.
      def with_ruleset(...) = Testing.with_ruleset(...)
    end

    # OfflineClient is an offline Client whose document a test can rewrite.
    class OfflineClient < Client
      def initialize(read, **)
        @read = read
        @document_mutex = Mutex.new
        @document = read.call
        begin
          Eval::Ruleset.decode(@document)
        rescue Eval::DecodeError, TypeError => e
          raise OfflineError, "offline ruleset: #{e.message}"
        end
        super(offline: -> { @document_mutex.synchronize { @document } }, **)
      end

      alias reload_document reload
      private :reload_document

      # set rewrites the flag key to serve value to everyone: one variation,
      # served as fallthrough, so the reason reads fallthrough. A key the
      # document lacks is added, typed by the value; an existing flag keeps
      # its type. on_change fires.
      def set(key, value)
        @document_mutex.synchronize do
          doc = JSON.parse(@document)
          Testing.set_flag(doc, key.to_s, value)
          @document = JSON.generate(doc)
        end
        reload_document
      end

      # reload re-reads the source the client was built from, discarding
      # what set changed. on_change fires with the flags that changed.
      def reload
        document = @read.call
        @document_mutex.synchronize { @document = document }
        reload_document
      end
    end
  end
end
