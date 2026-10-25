# frozen_string_literal: true

require_relative "test_helper"
require "net/http"
require "socket"

# Puma in cluster mode: the application boots in the master, which forks
# two workers. Each worker must run its own background thread from the
# moment it is forked, not from its first request, and flush what it
# counted when it shuts down.
class PumaTest < Minitest::Test
  include AppProcess

  def test_each_worker_runs_its_own_background_thread_after_fork_and_flushes_on_shutdown
    port = free_port
    out = File.join(@dir, "puma.log")
    output = -> { File.exist?(out) ? File.read(out, encoding: "UTF-8").scrub : "" }
    pid = Process.spawn(env(PORT: port.to_s), "bundle", "exec", "puma", "-C", "config/puma.rb",
                        chdir: APP, %i[out err] => out)

    # Before any request, each worker has connected its own stream, as has
    # the master the application booted in.
    workers = eventually(30) do
      found = output.call.scan(/Worker \d+ \(PID: (\d+)\) booted/).flatten.map(&:to_i)
      found if found.size == 2
    end
    assert workers, output.call
    connected = -> { logged(:sse_connected).map { |e| e["pid"] }.uniq }
    assert eventually(15) { (workers - connected.call).empty? && connected.call.size == 3 },
           "sse_connected from #{connected.call}, workers #{workers}\n#{output.call}"

    # A change reaches every worker over its own stream.
    @server.publish(document(2, enabled: false))
    seen = {}
    served = 0
    assert(eventually(15) do
      body = JSON.parse(Net::HTTP.get(URI("http://127.0.0.1:#{port}/flags/new_checkout")))
      served += 1
      seen[body["pid"]] = body["value"]
      seen.size == 2 && seen.values.none?
    end, "workers: #{seen}")

    # Shutdown closes each worker's client, which flushes its summary.
    Process.kill("TERM", pid)
    Process.wait(pid)
    pid = nil
    assert_equal served, summary_counts("new_checkout"), output.call
  ensure
    if pid
      Process.kill("KILL", pid)
      Process.wait(pid)
    end
  end

  private

  def free_port
    server = TCPServer.new("127.0.0.1", 0)
    server.addr[1]
  ensure
    server&.close
  end
end
