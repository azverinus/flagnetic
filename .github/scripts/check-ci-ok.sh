#!/usr/bin/env bash
# Checks that the ci-ok job of a workflow needs every other job in it. ci-ok
# is the only check branch protection requires, so a job it does not need
# could fail without blocking a merge.
set -euo pipefail

workflow="${1:-.github/workflows/ci.yml}"

# Job IDs: keys indented by two spaces under the top-level `jobs:` key.
jobs="$(awk '
  /^jobs:/ { in_jobs = 1; next }
  /^[^ #]/ { in_jobs = 0 }
  in_jobs && /^  [A-Za-z0-9_-]+:/ { sub(/^  /, ""); sub(/:.*/, ""); print }
' "$workflow" | { grep -vx 'ci-ok' || true; } | sort)"

# The one-line `needs: [a, b, c]` of the ci-ok job.
needs="$(awk '
  /^  ci-ok:/ { in_ok = 1; next }
  /^  [A-Za-z0-9_-]+:/ { in_ok = 0 }
  in_ok && /^    needs:/ { sub(/^    needs: *\[/, ""); sub(/\].*/, ""); gsub(/[ ,]+/, "\n"); print }
' "$workflow" | { grep -v '^$' || true; } | sort)"

if [[ -z "$needs" ]]; then
  echo "$workflow: no ci-ok job with a needs list written as [a, b, c]" >&2
  exit 1
fi

# Without always(), a failed job would skip ci-ok instead of failing it.
if ! awk '
  /^  ci-ok:/ { in_ok = 1; next }
  /^  [A-Za-z0-9_-]+:/ { in_ok = 0 }
  in_ok && /^    if: always\(\)/ { found = 1 }
  END { exit !found }
' "$workflow"; then
  echo "$workflow: ci-ok must run with if: always()" >&2
  exit 1
fi

missing="$(comm -23 <(echo "$jobs") <(echo "$needs"))"
if [[ -n "$missing" ]]; then
  echo "$workflow: ci-ok does not need these jobs:" >&2
  echo "$missing" | sed 's/^/  /' >&2
  exit 1
fi
echo "ok: ci-ok needs every job of $workflow"
