# frozen_string_literal: true

require "test_helper"

# A forked child (Puma cluster mode, Unicorn) does not inherit the parent's
# background thread: after_fork starts its own, and the child follows the
# stream while the parent keeps its own.
class ForkTest < Minitest::Test
  include ClientHelpers

  def setup
    skip "fork is not available" unless Process.respond_to?(:fork)
    super
  end

  # in_child forks a child that runs the block once the parent has run
  # parent, and returns what the block returned (as JSON).
  def in_child(parent)
    go_r, go_w = IO.pipe
    out_r, out_w = IO.pipe
    pid = fork do
      go_w.close
      out_r.close
      go_r.read
      out_w.write(JSON.generate(yield))
      out_w.close
      exit!(0)
    end
    go_r.close
    out_w.close
    parent.call
    go_w.close
    result = JSON.parse(out_r.read)
    Process.wait(pid)
    result
  end

  def test_after_fork_gives_the_child_its_own_stream
    @server.publish(document(1, { "on" => "one" }))
    c = client
    assert_equal :fresh, c.ready(timeout: 2)
    eventually { @server.streams.size == 1 }

    child = in_child(-> { @server.publish(document(2, { "on" => "two" })) }) do
      c.after_fork
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + 5
      while c.string("on", default: "") != "two" && Process.clock_gettime(Process::CLOCK_MONOTONIC) < deadline
        sleep 0.02
      end
      [c.string("on", default: ""), c.stage.to_s]
    end
    assert_equal %w[two fresh], child
    assert eventually { @server.stream_requests.size >= 2 }, "the child opened a stream of its own"
  end

  def test_the_parent_keeps_serving_after_a_child_closed_its_copy
    @server.publish(document(1, { "on" => "one" }))
    c = client
    c.ready(timeout: 2)
    in_child(-> {}) do
      c.after_fork
      c.close(timeout: 1)
      c.string("on", default: "")
    end
    @server.publish(document(2, { "on" => "two" }))
    assert(eventually { c.string("on", default: "") == "two" })
  end
end
