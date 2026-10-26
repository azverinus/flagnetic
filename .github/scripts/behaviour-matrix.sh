#!/usr/bin/env bash
# Rewrites one SDK's column of docs/sdks/behaviour-matrix.md from the report
# its protocol runner wrote: a JSON array of the names of the scenarios that
# passed in that run. A scenario is marked only when the runner says it
# passed, never because of its mode, so the page cannot claim a scenario a
# runner skipped. Every other column is kept as it is; a new SDK's column is
# added after the last one. CI runs it after each runner and fails on a
# difference from the committed page.
set -euo pipefail

column="${1:?usage: behaviour-matrix.sh <column, e.g. Ruby> <report>}"
report="${2:?usage: behaviour-matrix.sh <column, e.g. Ruby> <report>}"
root="$(cd "$(dirname "$0")/../.." && pwd)"
matrix="$root/docs/sdks/behaviour-matrix.md"
protocol="$root/conformance/protocol"

# Every scenario, scenarios.json first and then the other files in name
# order, each with its mode.
scenarios="$(
  for f in "$protocol/scenarios.json" "$protocol"/*-scenarios.json; do
    jq '[.scenarios[] | {name, mode}]' "$f"
  done | jq -s 'add'
)"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
jq -j -n \
  --rawfile md "$matrix" \
  --argjson scenarios "$scenarios" \
  --slurpfile report "$report" \
  --arg column "$column" '
  def cells: ltrimstr("|") | rtrimstr("|") | split("|") | map(gsub("^\\s+|\\s+$"; ""));
  "<!-- generated:matrix:begin -->" as $begin | "<!-- generated:matrix:end -->" as $end
  | ($report[0]) as $passed
  | ($scenarios | map(.name)) as $names
  | ($passed - $names) as $unknown
  | if ($unknown | length) > 0 then error("the report names scenarios no file holds: \($unknown | join("; "))") else . end
  | if ($md | contains($begin) and contains($end)) | not then error("no \($begin) ... \($end) in the matrix") else . end
  | ($md | split($begin)) as [$before, $rest]
  | ($rest | split($end)) as [$table, $after]
  | ($table | split("\n") | map(select(startswith("|")))) as $rows
  | (if ($rows | length) > 0 then $rows[0] | cells | .[2:] else [] end) as $old
  | ($old + ([$column] - $old)) as $columns
  | ($rows[2:] | map(cells | {key: .[0], value: .[2:]}) | from_entries) as $marked
  | [
      "| Scenario | Mode | \($columns | join(" | ")) |",
      "|---|---|\($columns | map("---|") | join(""))",
      ($scenarios[] as $s
        | [ $columns[] as $c
            | if $c == $column then (if $passed | index($s.name) then "✓" else "" end)
              else ($marked[$s.name][$old | index($c)] // "")
              end ]
        | "| \($s.name) | \($s.mode) | \(join(" | ")) |")
    ]
  | $before + $begin + "\n" + join("\n") + "\n" + $end + $after
' > "$tmp"
cat "$tmp" > "$matrix"
