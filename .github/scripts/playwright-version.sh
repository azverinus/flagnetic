#!/usr/bin/env bash
# Prints the one Playwright version a pnpm lockfile resolves. The browser
# jobs run in the Playwright image of exactly that version, whose browsers
# are the builds that version drives, so CI takes the image tag from here
# and a dependency update moves both at once. More than one version, or
# none, fails: the image could match only one of them.
set -euo pipefail

lockfile="${1:?usage: playwright-version.sh <pnpm-lock.yaml>}"

versions="$({ grep -oE '^  playwright@[0-9]+\.[0-9]+\.[0-9]+:' "$lockfile" || true; } |
  sed -E 's/^  playwright@//; s/:$//' | sort -u)"

case "$(grep -c . <<<"$versions")" in
  1) echo "$versions" ;;
  *)
    echo "$lockfile: expected exactly one playwright version, found: ${versions:-none}" >&2
    exit 1
    ;;
esac
