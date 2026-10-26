# frozen_string_literal: true

require "test_helper"

# The schema range this SDK reads and the SDK API path it speaks, which
# docs/sdks/compatibility.md lists per SDK line.
class VersionTest < Minitest::Test
  TABLE = File.expand_path("../../../docs/sdks/compatibility.md", __dir__)

  def test_the_schema_range_and_the_sdk_api_path_are_declared
    assert_equal 1, Flagnetic::SCHEMA_VERSION_MIN
    assert_equal 1, Flagnetic::SCHEMA_VERSION_MAX
    assert_equal "/sdk/v1", Flagnetic::SDK_API_PATH
  end

  # The evaluator compiles one schema; the SDK cannot read another.
  def test_the_highest_schema_is_the_evaluators
    assert_equal Flagnetic::Eval::SCHEMA_VERSION, Flagnetic::SCHEMA_VERSION_MAX
  end

  # A release that changes the range or the path needs its row first, so
  # the page can never promise a server what this version does not speak.
  def test_the_compatibility_table_row_covering_this_version_says_what_it_declares
    row = covering(ruby_rows, Flagnetic::VERSION)
    refute_nil row, "no Ruby row of #{TABLE} covers #{Flagnetic::VERSION}"
    min = Flagnetic::SCHEMA_VERSION_MIN
    max = Flagnetic::SCHEMA_VERSION_MAX
    range = min == max ? max.to_s : "#{min}–#{max}"
    assert_equal [range, "`#{Flagnetic::SDK_API_PATH}`"], [row[:schema], row[:path]]
  end

  # A new SDK API path is a new major version.
  def test_one_major_speaks_one_sdk_api_path
    ruby_rows.group_by { |r| r[:major] }.each do |major, rows|
      assert_equal 1, rows.map { |r| r[:path] }.uniq.size, "Ruby #{major}.x rows name more than one SDK API path"
    end
  end

  def test_a_row_covers_the_versions_it_names
    rows = [row("0.x"), row("≥ 0.3.0")]
    assert_equal "0.x", covering(rows, "0.1.0")[:version]
    assert_equal "≥ 0.3.0", covering(rows, "0.3.1")[:version]
    assert_nil covering(rows, "1.0.0")
  end

  private

  # ruby_rows are the table's Ruby rows: `N.x` covers every N.y.z,
  # `≥ N.y.z` covers N.y.z and later within major N.
  def ruby_rows
    File.readlines(TABLE, chomp: true, encoding: "UTF-8").filter_map do |line|
      cells = line.split("|").map(&:strip).drop(1)
      row(cells[1], cells[2], cells[3]) if cells[0] == "Ruby"
    end
  end

  def row(version, schema = "1", path = "`/sdk/v1`")
    major = version[/\A(\d+)\.x\z/, 1]
    from = major ? [major.to_i, 0, 0] : version.delete_prefix("≥ ").split(".").map(&:to_i)
    { version: version, schema: schema, path: path, major: from[0], from: from }
  end

  # covering is the row that applies to version: of the rows of its major
  # that start at or before it, the one starting latest.
  def covering(rows, version)
    v = version.split(".").map(&:to_i)
    rows.select { |r| r[:major] == v[0] && (r[:from] <=> v) <= 0 }.max_by { |r| r[:from] }
  end
end
