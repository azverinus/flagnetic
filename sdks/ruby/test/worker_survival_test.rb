# frozen_string_literal: true

require "test_helper"
require "timeout"

# The background thread is the only thing that keeps a process's ruleset
# current, so nothing a fetch, a listener or a comparison raises may end it,
# and closing the client never hands such a failure to the host.
class WorkerSurvivalTest < Minitest::Test
  include ClientHelpers

  Fatal = Class.new(Exception) # rubocop:disable Lint/InheritException

  def test_an_exception_outside_standard_error_does_not_end_the_worker
    @server.publish(document(1, { "on" => true }))
    c = client(stream: false, timing: { poll_down: 0.1 })
    raised = false
    c.define_singleton_method(:handle_fetch) do |response|
      unless raised
        raised = true
        raise Fatal, "boom"
      end
      super(response)
    end

    assert(eventually { !logged(:worker_failed).empty? })
    assert_equal :fresh, c.ready(timeout: 5)
    assert_match(/Fatal: boom/, logged(:worker_failed).first[1][:cause])
    @server.publish(document(2, { "on" => false }))
    assert eventually(5) { c.bool("on", default: true) == false }
  end

  def test_a_dead_worker_is_logged_and_restarted
    @server.publish(document(1, { "on" => true }))
    c = client(stream: false, timing: { poll_down: 0.1 })
    assert_equal :fresh, c.ready(timeout: 5)
    worker = c.instance_variable_get(:@worker)
    broken = true
    worker.define_singleton_method(:step) do
      raise Fatal, "dead" if broken

      super()
    end
    worker.wake
    assert eventually(5) { !logged(:worker_died).empty? }
    broken = false
    @server.publish(document(2, { "on" => false }))
    assert eventually(10) { c.bool("on", default: true) == false }
  end

  def test_close_never_raises_what_the_worker_died_of
    @server.publish(document(1, { "on" => true }))
    c = client(stream: false)
    assert_equal :fresh, c.ready(timeout: 5)
    worker = c.instance_variable_get(:@worker)
    worker.instance_variable_get(:@thread).kill
    worker.instance_variable_get(:@thread).join
    thread = Thread.new { raise Fatal, "late" }
    thread.report_on_exception = false
    worker.instance_variable_set(:@thread, thread)
    sleep 0.05
    assert_nil c.close(timeout: 1)
  end

  def test_close_lets_an_exception_raised_into_the_caller_through
    @server.publish(document(1, { "on" => true }))
    c = client(stream: false)
    assert_equal :fresh, c.ready(timeout: 5)
    worker = c.instance_variable_get(:@worker)
    stuck = Thread.new { sleep }
    worker.instance_variable_set(:@thread, stuck)
    assert_raises(Timeout::Error) { Timeout.timeout(0.2) { c.close(timeout: 5) } }
  ensure
    stuck&.kill
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
