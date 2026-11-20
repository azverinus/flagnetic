# frozen_string_literal: true

require_relative "test_helper"
require "net/http"
require "socket"

# Puma in cluster mode: the application boots in the master, which forks
# two workers. A worker runs its own background thread from its first call,
# not from the fork, so a worker that never evaluates opens no stream; it
# flushes what it counted when it shuts down.
class PumaTest < Minitest::Test
  include AppProcess

  def test_each_worker_runs_its_own_background_thread_from_its_first_call_and_flushes_on_shutdown
    port = free_port
    out = File.join(@dir, "puma.log")
    output = -> { File.exist?(out) ? File.read(out, encoding: "UTF-8").scrub : "" }
    pid = Process.spawn(env(PORT: port.to_s), "bundle", "exec", "puma", "-C", "config/puma.rb",
                        chdir: APP, %i[out err] => out)

    # Before any request only the master, which the application booted in,
    # has connected a stream: the workers were forked and evaluated nothing.
    workers = eventually(30) do
      found = output.call.scan(/Worker \d+ \(PID: (\d+)\) booted/).flatten.map(&:to_i)
      found if found.size == 2
    end
    assert workers, output.call
    connected = -> { logged(:sse_connected).map { |e| e["pid"] }.uniq }
    assert eventually(15) { connected.call.size == 1 }, "sse_connected from #{connected.call}\n#{output.call}"
    sleep 1
    assert_equal 1, connected.call.size, "a worker opened a stream before it evaluated: #{connected.call}"

    # Each worker's first request starts its own stream, and a change then
    # reaches every worker over it.
    @server.publish(document(2, enabled: false))
    seen = {}
    served = 0
    assert(eventually(15) do
      body = JSON.parse(Net::HTTP.get(URI("http://127.0.0.1:#{port}/flags/new_checkout")))
      served += 1
      seen[body["pid"]] = body["value"]
      seen.size == 2 && seen.values.none?
    end, "workers: #{seen}")
    assert_empty workers - connected.call, "sse_connected from #{connected.call}, workers #{workers}"

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
