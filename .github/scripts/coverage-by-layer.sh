#!/usr/bin/env bash
# Prints a Markdown table of statement coverage per layer of the server from a
# Go coverage profile, for the CI job summary. The floors themselves are
# checked by go-test-coverage against .testcoverage.yml; the paths left out
# here are the ones it excludes (the entry point and the test helpers).
set -euo pipefail

profile="${1:-coverage.out}"

echo "| Layer | Statements | Covered |"
echo "|---|---:|---:|"
awk '
  NR == 1 { next } # mode line
  {
    # A block appears once per test binary with -coverpkg; it counts once,
    # covered if any binary ran it.
    split($1, loc, ":")
    file = loc[1]
    sub(/^github\.com\/azverinus\/flagnetic\//, "", file)
    if (file !~ /^internal\// || file ~ /^internal\/(testkit|pkg\/pgtest)\// || file ~ /\.gen\.go$/) next
    split(file, parts, "/")
    layer = parts[1] "/" parts[2]
    stmts[$1] = $2
    layerOf[$1] = layer
    if ($3 > 0) covered[$1] = 1
  }
  END {
    for (b in stmts) {
      total[layerOf[b]] += stmts[b]
      if (b in covered) hit[layerOf[b]] += stmts[b]
    }
    for (l in total) printf "| `%s` | %d | %.1f %% |\n", l, total[l], 100 * hit[l] / total[l]
  }
' "$profile" | sort
