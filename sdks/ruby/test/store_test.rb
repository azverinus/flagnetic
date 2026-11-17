# frozen_string_literal: true

require "test_helper"
require "tmpdir"
require "minitest/mock"

# The file store's write: a fresh, unguessable temporary file beside the
# target, created only if nothing is there, readable by the owner only and
# renamed over the target, so a file or symlink planted in the directory is
# never written through.
class StoreTest < Minitest::Test
  KEY = "fn_srv_store_test"

  def store(path) = Flagnetic.const_get(:FileStore).new(path, KEY)

  def test_a_symlink_planted_at_a_guessable_temporary_name_is_not_followed
    Dir.mktmpdir do |dir|
      path = File.join(dir, "ruleset.json")
      victim = File.join(dir, "victim")
      File.write(victim, "untouched")
      # Every name a writer could predict from the process and the thread.
      planted = ["#{path}.#{Process.pid}.#{Thread.current.object_id}.tmp", "#{path}.tmp", "#{path}.#{Process.pid}.tmp"]
      planted.each { |name| File.symlink(victim, name) }

      store(path).save('{"version":1}')

      assert_equal "untouched", File.read(victim)
      assert(planted.all? { |name| File.symlink?(name) })
      assert_equal '{"version":1}', store(path).load.first
      refute File.symlink?(path)
    end
  end

  def test_the_store_is_readable_by_the_owner_only_and_leaves_no_temporary_file
    Dir.mktmpdir do |dir|
      path = File.join(dir, "ruleset.json")
      store(path).save('{"version":1}')
      store(path).save('{"version":2}')
      assert_equal 0o600, File.stat(path).mode & 0o777
      assert_equal ["ruleset.json"], Dir.children(dir)
      assert_equal '{"version":2}', store(path).load.first
    end
  end

  def test_two_writes_never_share_a_temporary_file
    Dir.mktmpdir do |dir|
      path = File.join(dir, "ruleset.json")
      names = []
      rename = File.method(:rename)
      File.stub(:rename, lambda { |from, to|
        names << from
        rename.call(from, to)
      }) do
        2.times { store(path).save("{}") }
      end
      assert_equal 2, names.uniq.size
      assert(names.all? { |n| File.dirname(n) == dir })
    end
  end

  def test_a_failed_write_removes_its_temporary_file_and_keeps_the_target
    Dir.mktmpdir do |dir|
      path = File.join(dir, "ruleset.json")
      store(path).save('{"version":1}')
      # A directory in the target's place makes the rename fail.
      blocked = File.join(dir, "blocked")
      Dir.mkdir(blocked)
      File.write(File.join(blocked, "x"), "")
      assert_raises(SystemCallError) { store(blocked).save('{"version":2}') }
      assert_equal %w[blocked ruleset.json], Dir.children(dir).sort
      assert_equal '{"version":1}', store(path).load.first
    end
  end
end
