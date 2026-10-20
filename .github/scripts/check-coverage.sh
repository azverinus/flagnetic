#!/usr/bin/env bash
# Prints the statement coverage of every package in a Go cover profile as a
# Markdown table, and fails when a package of the module has a floor in the
# floors file (lines of "<module> <package> <percent>", directories relative
# to the repository root) and is below it or missing from the profile.
set -euo pipefail

usage="usage: check-coverage.sh <module directory> <coverprofile> <floors file>"
module="${1:?$usage}"
profile="${2:?$usage}"
floors="${3:?$usage}"
prefix="github.com/azverinus/flagnetic/"

awk -v module="$module" -v prefix="$prefix" '
  FILENAME == ARGV[1] {
    if ($0 !~ /^#/ && NF == 3 && $1 == module) floor[$2] = $3
    next
  }
  FNR == 1 { next } # the mode: line
  {
    file = substr($1, 1, index($1, ":") - 1)
    dir = file
    sub(/\/[^\/]*$/, "", dir)
    if (index(dir, prefix) == 1) dir = substr(dir, length(prefix) + 1)
    total[dir] += $(NF - 1)
    if ($NF > 0) covered[dir] += $(NF - 1)
  }
  END {
    print "| Package | Coverage | Floor |"
    print "|---|---|---|"
    failed = 0
    n = sorted_keys(total, dirs)
    for (i = 1; i <= n; i++) {
      d = dirs[i]
      pct = total[d] ? 100 * covered[d] / total[d] : 100
      if (d in floor) {
        mark = ""
        if (pct < floor[d]) { mark = " ❌"; failed = 1 }
        printf "| `%s` | %.1f %% | %s %%%s |\n", d, pct, floor[d], mark
      } else {
        printf "| `%s` | %.1f %% | none |\n", d, pct
      }
    }
    for (d in floor) {
      if (!(d in total)) {
        printf "| `%s` | not in the profile ❌ | %s %% |\n", d, floor[d]
        failed = 1
      }
    }
    exit failed
  }
  # Sorts the keys of a into b (1-based) without gawk extensions.
  function sorted_keys(a, b,   k, n, i, j, t) {
    n = 0
    for (k in a) b[++n] = k
    for (i = 2; i <= n; i++) {
      t = b[i]
      for (j = i - 1; j >= 1 && b[j] > t; j--) b[j + 1] = b[j]
      b[j + 1] = t
    }
    return n
  }
' "$floors" "$profile"
