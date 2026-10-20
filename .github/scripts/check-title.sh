#!/usr/bin/env bash
# Checks that a pull request title is a Conventional Commit subject:
#   type(scope)!: summary
set -euo pipefail

title="${1:?usage: check-title.sh <title>}"
types='feat|fix|perf|deps|refactor|test|docs|build|ci|chore|revert'
pattern="^(${types})(\([a-z0-9-]+\))?!?: [^ ].{0,71}$"

if [[ "$title" =~ $pattern ]]; then
  echo "ok: $title"
  exit 0
fi

cat >&2 <<MSG
Pull request title is not a Conventional Commit subject:
  $title
Expected: type(scope): summary, at most 72 characters after the colon.
Types: ${types//|/, }. Add ! before the colon for a breaking change.
MSG
exit 1
