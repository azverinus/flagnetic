# frozen_string_literal: true

module Flagnetic
  # FileStore keeps the last good ruleset in one file: the ruleset document
  # exactly as the endpoint served it, so the file also works as an offline
  # document. Its modification time is when the ruleset was received.
  # Writes go to a unique temporary file renamed over the target, so
  # processes sharing the path (Puma workers) never read a torn file.
  class FileStore
    def initialize(path)
      @path = path.to_s
    end

    # load returns [json, received_at], or nil when there is no file.
    def load
      json = File.binread(@path)
      [json, File.mtime(@path)]
    rescue Errno::ENOENT
      nil
    end

    def save(json)
      tmp = "#{@path}.#{Process.pid}.#{Thread.current.object_id}.tmp"
      # The server ruleset holds private segments: readable by the owner only.
      File.open(tmp, File::WRONLY | File::CREAT | File::TRUNC | File::BINARY, 0o600) { |f| f.write(json) }
      File.rename(tmp, @path)
    ensure
      File.delete(tmp) if tmp && File.exist?(tmp)
    end

    def to_s = @path
  end
end
