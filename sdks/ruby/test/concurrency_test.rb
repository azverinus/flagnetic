# frozen_string_literal: true

require "test_helper"

# One client shared by threads and surviving fork: evaluations never lock
# and never see a half-swapped ruleset, every evaluation of every thread is
# counted, and a forked child runs its own background thread with its own
# event queue.
class ConcurrencyTest < Minitest::Test
  include ClientHelpers

  # versioned is a ruleset whose flag "version" serves its own version.
  def versioned(version) = document(version, { "version" => version, "on" => true })

  def test_threads_evaluate_while_rulesets_swap
    @server.publish(versioned(1))
    c = client
    assert_equal :fresh, c.ready(timeout: 2)

    stop = false
    mismatches = Queue.new
    evaluations = Queue.new
    readers = Array.new(8) do |i|
      Thread.new do
        n = 0
        until stop
          d = c.detail("version", default: -1, context: { units: { user: "u-#{i}-#{n}" } })
          mismatches << d.to_h if d.value != d.ruleset_version || d.reason != :fallthrough
          n += 1
          Thread.pass if (n % 50).zero?
        end
        evaluations << n
      end
    end
    (2..20).each do |v|
      @server.publish(versioned(v))
      sleep 0.02
    end
    eventually { c.number("version", default: 0) == 20 }
    stop = true
    readers.each(&:join)

    assert_equal 20, c.number("version", default: 0)
    assert_empty Array.new(mismatches.size) { mismatches.pop }
    assert_operator Array.new(evaluations.size) { evaluations.pop }.sum, :>, 1000
  end

  def test_every_evaluation_of_every_thread_is_counted
    @server.publish(document(1, { "on" => true }))
    c = client(summary_interval: 3600, stream: false)
    c.ready(timeout: 2)
    Array.new(8) { Thread.new { 500.times { c.bool("on", default: false) } } }.each(&:join)
    c.flush(timeout: 2)
    counts = @server.events_requests.flat_map(&:events).select { |e| e["type"] == "evaluation_summary" }
                    .flat_map { |e| e["counts"] }
    assert_equal(4000, counts.sum { |r| r["count"] })
  end

  def test_a_forked_child_restarts_the_background_thread_on_its_next_call
    skip "fork is unavailable on #{RUBY_PLATFORM}" unless Process.respond_to?(:fork)

    @server.publish(versioned(1))
    c = client
    assert_equal :fresh, c.ready(timeout: 2)
    eventually { @server.streams.size == 1 }
    streams = @server.stream_requests.size

    from_child, to_parent = IO.pipe
    from_parent, to_child = IO.pipe
    pid = fork do
      from_child.close
      to_child.close
      report = lambda { |line|
        to_parent.puts(line)
        to_parent.flush
      }
      # Threads of the child evaluate from the inherited ruleset while the
      # first of them restarts the background thread.
      threads = Array.new(4) { Thread.new { c.number("version", default: 0) } }
      report.call("inherited #{threads.map(&:value).uniq.join(",")}")
      from_parent.gets # the parent published version 2
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + 3
      sleep 0.01 until c.number("version", default: 0) == 2 ||
                       Process.clock_gettime(Process::CLOCK_MONOTONIC) > deadline
      report.call("seen #{c.number("version", default: 0)} #{c.stage}")
      c.close(timeout: 1)
      exit!(0)
    end
    to_parent.close
    from_parent.close

    assert_equal "inherited 1", from_child.gets&.chomp
    eventually { @server.stream_requests.size == streams + 1 } # the child's own stream
    assert_equal streams + 1, @server.stream_requests.size
    assert_equal "1", @server.stream_requests.last["last-event-id"]

    @server.publish(versioned(2))
    to_child.puts("published")
    to_child.flush
    assert_equal "seen 2 fresh", from_child.gets&.chomp
    _, status = Process.wait2(pid)
    assert_predicate status, :success?

    eventually { c.number("version", default: 0) == 2 }
    assert_equal 2, c.number("version", default: 0), "the parent's thread kept running"
  ensure
    [from_child, to_child].each { |io| io&.close unless io&.closed? }
  end

  # A child inherits the parent's queue and window in memory; sending them
  # from both processes would report the parent's events twice, so the
  # child starts empty and the parent keeps its own.
  def test_a_forked_child_sends_only_its_own_events
    skip "fork is unavailable on #{RUBY_PLATFORM}" unless Process.respond_to?(:fork)

    @server.publish(document(1, { "on" => true }))
    c = client(flush_interval: 3600, summary_interval: 3600, stream: false)
    c.ready(timeout: 2)
    c.alias("anonymous", "d-parent", "user", "u-parent")
    c.bool("on", default: false)

    pid = fork do
      c.after_fork
      c.alias("anonymous", "d-child", "user", "u-child")
      c.close(timeout: 2)
      exit!(0)
    end
    _, status = Process.wait2(pid)
    assert_predicate status, :success?
    child = @server.events_requests.flat_map(&:events)
    assert_equal([%w[alias d-child]], child.map { |e| [e["type"], e["from_unit_id"]] })

    c.flush(timeout: 2)
    parent = @server.events_requests.flat_map(&:events) - child
    assert_equal %w[alias evaluation_summary], parent.map { |e| e["type"] }.sort
    assert_equal "d-parent", parent.find { |e| e["type"] == "alias" }["from_unit_id"]
  end
end
