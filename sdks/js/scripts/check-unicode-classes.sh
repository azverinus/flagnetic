#!/usr/bin/env bash
# Checks that src/evaluator/unicode-classes.ts is what the generator prints
# with the installed Go toolchain, so the \p{name} classes the regular
# expression engine accepts are the ones Go's regexp/syntax accepts.
set -euo pipefail

cd "$(dirname "$0")/.."

fresh="$(mktemp)"
trap 'rm -f "$fresh"' EXIT
go run scripts/gen-unicode-classes.go > "$fresh"
if ! diff -u src/evaluator/unicode-classes.ts "$fresh"; then
  echo "src/evaluator/unicode-classes.ts is stale: run pnpm gen:unicode and commit the result" >&2
  exit 1
fi
echo "ok: the Unicode class tables are what the generator prints"
