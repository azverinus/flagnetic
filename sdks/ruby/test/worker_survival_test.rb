# frozen_string_literal: true

require "test_helper"
require "timeout"

# The background thread is the only thing that keeps a process's ruleset
# current, so nothing a fetch, a listener or a comparison raises may end it,
# and closing the client never hands such a failure to the host.
class WorkerSurvivalTest < Minitest::Test
  include ClientHelpers

  Fatal = Class.new(Exception) # rubocop:disable Lint/InheritException

  # A change listener runs on the worker's thread, so one that raises
  # something that is not a StandardError is how the public API shows the
  # worker a failure that its own loop must survive.
  def raising_once(error)
    raised = false
    lambda do |_keys|
      next if raised

      raised = true
      raise error
    end
  end

  def test_an_exception_outside_standard_error_does_not_end_the_worker
    @server.publish(document(1, { "on" => true }))
    c = client(stream: false, timing: { poll_down: 0.1 })
    assert_equal :fresh, c.ready(timeout: 5)
    c.on_change(&raising_once(Fatal.new("boom")))

    @server.publish(document(2, { "on" => false }))
    assert(eventually { !logged(:worker_failed).empty? })
    assert_match(/Fatal: boom/, logged(:worker_failed).first[1][:cause])
    @server.publish(document(3, { "on" => true }))
    assert eventually(5) { c.detail("on", default: false).ruleset_version == 3 }
  end

  def test_a_dead_worker_is_logged_and_restarted
    @server.publish(document(1, { "on" => true }))
    c = client(stream: false, timing: { poll_down: 0.1 })
    assert_equal :fresh, c.ready(timeout: 5)
    # SystemExit is the one failure the loop's step lets through to its supervisor.
    c.on_change(&raising_once(SystemExit.new))
    @server.publish(document(2, { "on" => false }))
    assert eventually(5) { !logged(:worker_died).empty? }
    @server.publish(document(3, { "on" => true }))
    assert eventually(10) { c.detail("on", default: false).ruleset_version == 3 }
  end

  def test_close_never_raises_what_the_worker_died_of
    server = @server
    server.publish(document(1, { "on" => true }))
    # A logger that fails on the supervisor's own report ends the thread with it.
    dying = lambda do |level, event, fields|
      record(level, event, fields)
      raise Fatal, "late" if event == :worker_died
    end
    c = Flagnetic::Client.new(KEY, base_url: server.base_url, logger: dying, stream: false,
                                   timing: FAST.merge(poll_down: 0.1))
    @clients << c
    assert_equal :fresh, c.ready(timeout: 5)
    c.on_change(&raising_once(SystemExit.new))
    server.publish(document(2, { "on" => false }))
    assert eventually(5) { !logged(:worker_died).empty? }
    sleep 0.05
    assert_nil c.close(timeout: 1)
  end

  def test_close_lets_an_exception_raised_into_the_caller_through
    @server.publish(document(1, { "on" => true }))
    c = client(stream: false, timing: { poll_down: 0.1 })
    assert_equal :fresh, c.ready(timeout: 5)
    # The worker is held in a listener, so close waits on it.
    entered = Queue.new
    c.on_change do |_keys|
      entered << true
      sleep
    end
    @server.publish(document(2, { "on" => false }))
    assert entered.pop(timeout: 5)
    assert_raises(Timeout::Error) { Timeout.timeout(0.2) { c.close(timeout: 5) } }
  end

  # Overflowing the stack depends on the platform's stack size, so the
  # item whose comparison overflows is built rather than nested.
  def test_changes_reports_an_item_whose_comparison_overflows_the_stack_as_changed
    overflowing = Struct.new(:key) do
      def ==(_other) = raise(SystemStackError, "stack level too deep")
    end
    before = [overflowing.new("k"), Struct.new(:key).new("same")]
    after = [overflowing.new("k"), before.last]

    changed = Flagnetic.const_get(:Changes).changed(before, after, &:key)
    assert_equal ["k"], changed.keys
  end
end
