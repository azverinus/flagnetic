# frozen_string_literal: true

require "digest"
require "json"

module Flagnetic
  # FileStore keeps the last good ruleset in one file: a header line naming
  # the key the ruleset was received under, then the ruleset document exactly
  # as the endpoint served it. The header holds a digest of the SDK key, never
  # the key. Without it, a process restarted with another environment's key on
  # the same path would serve that environment's ruleset as last_good, and
  # refuse the right one as a downgrade whenever the old version was higher.
  # The file's modification time is when the ruleset was received.
  # Writes go to a unique temporary file renamed over the target, so
  # processes sharing the path (Puma workers) never read a torn file.
  class FileStore
    FORMAT = 1

    # Refused is raised for a file this client may not serve from: one written
    # under no key, under another key or in another format.
    class Refused < StandardError; end

    def initialize(path, sdk_key)
      @path = path.to_s
      @digest = Digest::SHA256.hexdigest(sdk_key.to_s)
    end

    # load returns [json, received_at], or nil when there is no file. It
    # raises Refused for a file written under no key or another key.
    def load
      content = File.binread(@path)
      received_at = File.mtime(@path)
      [document_of(content), received_at]
    rescue Errno::ENOENT
      nil
    end

    def save(json)
      tmp = "#{@path}.#{Process.pid}.#{Thread.current.object_id}.tmp"
      # The server ruleset holds private segments: readable by the owner only.
      File.open(tmp, File::WRONLY | File::CREAT | File::TRUNC | File::BINARY, 0o600) do |f|
        f.write(header, "\n", json)
      end
      File.rename(tmp, @path)
    ensure
      File.delete(tmp) if tmp && File.exist?(tmp)
    end

    def to_s = @path

    private

    def header = JSON.generate({ "flagnetic_store" => FORMAT, "key" => @digest })

    def document_of(content)
      line, newline, document = content.partition("\n".b)
      head = newline.empty? ? nil : parse_header(line)
      raise Refused, "the store was written under no key" unless head.is_a?(Hash) && head.key?("flagnetic_store")

      unless format?(head["flagnetic_store"])
        raise Refused,
              "the store was written in a format this client does not read"
      end
      raise Refused, "the store was written under another key" unless head["key"] == @digest

      document
    end

    def format?(value) = value.is_a?(Integer) && value == FORMAT

    def parse_header(line)
      JSON.parse(line.dup.force_encoding(Encoding::UTF_8))
    rescue JSON::ParserError, EncodingError
      nil
    end
  end
  private_constant :FileStore
end
