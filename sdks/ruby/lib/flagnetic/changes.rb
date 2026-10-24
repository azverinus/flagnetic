# frozen_string_literal: true

module Flagnetic
  # Changes finds the flags whose evaluation may differ between two
  # rulesets: flags added, removed or reconfigured, and flags that reach,
  # through their conditions, the conditions of the segments those name,
  # their overrides, rollouts, experiments and prerequisites, a segment, a
  # declared attribute or unit type, or a prerequisite flag that changed,
  # transitively. A flag's own definition names its segments and
  # prerequisites but not their contents, so comparing definitions alone
  # misses a segment edit that flips a value.
  module Changes
    module_function

    # flags returns the sorted keys; before nil means every flag of after.
    def flags(before, after)
      return after.flags.map(&:key).sort if before.nil?

      segments = changed(before.segments, after.segments, &:key)
      names = changed(before.attributes, after.attributes, &:key)
              .merge(changed(before.unit_types, after.unit_types, &:itself))
      affected = changed(before.flags, after.flags, &:key)
      spread(affected, reaches(before, after), segments, names)
      affected.keys.sort
    end

    # spread adds to affected every flag reaching a changed segment or name,
    # or an affected prerequisite, until nothing more is.
    def spread(affected, reaches, segments, names)
      loop do
        grew = reaches.select do |key, r|
          !affected[key] && (r[:segments].any? { |s| segments[s] } || r[:names].any? { |n| names[n] } ||
                             r[:flags].any? { |f| affected[f] })
        end
        return if grew.empty?

        grew.each_key { |k| affected[k] = true }
      end
    end

    # changed returns the keys of items added, removed or changed, as a
    # Hash used as a set. Decoded items are frozen structs compared by
    # value.
    def changed(before, after, &key)
      a = before.to_h { |i| [key.call(i), i] }
      b = after.to_h { |i| [key.call(i), i] }
      out = {}
      a.each { |k, v| out[k] = true unless b.key?(k) && b[k] == v }
      b.each_key { |k| out[k] = true unless a.key?(k) }
      out
    end

    # reaches maps every flag of either ruleset to what its evaluation
    # reads: segments (transitively through segment conditions), attribute
    # and unit type names, and prerequisite flags.
    def reaches(*rulesets)
      out = Hash.new { |h, k| h[k] = { segments: {}, names: {}, flags: {} } }
      rulesets.each do |rs|
        by_segment = rs.segments.to_h { |s| [s.key, s] }
        rs.flags.each do |f|
          r = out[f.key]
          f.prerequisites.each { |p| r[:flags][p.flag] = true }
          f.overrides.each { |o| r[:names][o.unit_type] = true }
          f.rules.each do |rule|
            walk(rule.conditions, r, by_segment)
            r[:names][rule.serve.unit_type] = true
          end
        end
      end
      out.transform_values { |r| r.transform_values(&:keys) }
    end

    def walk(conditions, reach, by_segment)
      conditions.each do |c|
        case c.kind
        when "in_segment", "not_in_segment"
          next if reach[:segments][c.segment]

          reach[:segments][c.segment] = true
          walk(by_segment[c.segment]&.conditions || [], reach, by_segment)
        when "prerequisite" then reach[:flags][c.flag] = true
        else reach[:names][c.attribute] = true # an attribute, or a unit type read as one
        end
      end
    end
  end
  private_constant :Changes
end
