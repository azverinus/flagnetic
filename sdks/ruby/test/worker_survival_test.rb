# frozen_string_literal: true

require "test_helper"

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

  def test_changes_survives_deeply_nested_variation_values
    deep = 9000.times.reduce("x") { |acc, _| [acc] }
    mk = lambda do |value|
      flag = { "key" => "k", "type" => "json", "on" => true, "off_variation" => "v", "fallthrough" => "v",
               "variations" => [{ "key" => "v", "value" => value }] }
      doc = document(1).merge("flags" => [flag])
      Flagnetic::Eval::Ruleset.decode(JSON.generate(doc, max_nesting: false))
    end
    a = mk.call(deep)
    b = mk.call(deep)
    assert_equal ["k"], Flagnetic.const_get(:Changes).flags(a, b)
  end
end
