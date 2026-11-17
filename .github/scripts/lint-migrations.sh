#!/usr/bin/env bash
# Lints the migrations a change adds with squawk, so that a replica of the
# previous minor keeps working against the schema the new one migrates to
# (expand/contract: add, never rename or drop what the previous minor uses, and
# take no lock that stops it for long). Settings are in .squawk.toml.
#
#   lint-migrations.sh --base REF   the migrations added since the merge base with REF
#   lint-migrations.sh FILE...      those files
#   lint-migrations.sh --self-test  each deliberately bad migration under
#                                   .github/scripts/testdata/migration-lint must fail
#
# Only added files are linted: a released migration is never edited, and the
# ones already there were written before the rule. An exception is a squawk
# ignore comment, `-- squawk-ignore <rule>`, on the line after a comment that
# gives the reason:
#
#   -- The column was never read by any release, so dropping it breaks no replica.
#   -- squawk-ignore ban-drop-column
#   ALTER TABLE flags DROP COLUMN legacy_name;
#
# squawk comes from $SQUAWK or the PATH. Without it the script exits 3: the
# check was not run, which is not a pass (install-squawk.sh fetches it).
# Exit 0: clean, 1: findings, 2: bad usage, 3: squawk is missing.
set -uo pipefail

MIGRATIONS=internal/infra/out/postgres/migrations
FIXTURE_DIR=.github/scripts/testdata/migration-lint

root="$(git rev-parse --show-toplevel)" || exit 2
cd "$root" || exit 2

# An ignore comment must say why on the line before it: a bare one hides a
# finding without telling the reviewer what makes it safe.
ignores_have_reasons() { # <file>
  awk '
    /^[[:space:]]*--[[:space:]]*squawk-ignore/ {
      if (prev !~ /^[[:space:]]*--/ || prev ~ /^[[:space:]]*--[[:space:]]*squawk-ignore/ || prev ~ /^[[:space:]]*--[[:space:]]*$/ || prev ~ /^[[:space:]]*--[[:space:]]*\+goose/) {
        printf "%s:%d: a squawk-ignore needs a comment line before it that gives the reason\n", FILENAME, FNR
        bad = 1
      }
    }
    { prev = $0 }
    END { exit bad }
  ' "$1"
}

lint_files() { # <file>...
  local f rc=0
  for f in "$@"; do ignores_have_reasons "$f" >&2 || rc=1; done
  "${SQUAWK:-squawk}" --config .squawk.toml "$@" || rc=1
  return $rc
}

squawk_available() { command -v "${SQUAWK:-squawk}" >/dev/null 2>&1; }

self_test() {
  local f rc=0
  for f in "$FIXTURE_DIR"/*.sql; do
    if lint_files "$f" >/dev/null 2>&1; then
      echo "lint-migrations: $f is meant to fail the lint and passed: the rules do not catch what they are for" >&2
      rc=1
    fi
  done
  for f in "$FIXTURE_DIR"/ok/*.sql; do
    if ! lint_files "$f"; then
      echo "lint-migrations: $f is meant to pass the lint and failed" >&2
      rc=1
    fi
  done
  return $rc
}

mode=files
base=""
files=()
while [ $# -gt 0 ]; do
  case "$1" in
    --base) shift; base="${1:?--base needs a ref}"; mode=base ;;
    --self-test) mode=self-test ;;
    -*) echo "lint-migrations: unknown argument $1" >&2; exit 2 ;;
    *) files+=("$1") ;;
  esac
  shift
done

squawk_available || { echo "lint-migrations: squawk is not installed (.github/scripts/install-squawk.sh DIR installs the pinned one); migrations not linted" >&2; exit 3; }

case "$mode" in
  self-test) self_test; exit $? ;;
  base)
    merge_base="$(git merge-base HEAD "$base")" || { echo "lint-migrations: no merge base with $base" >&2; exit 2; }
    # Added files only, committed or not; a rename counts as an addition.
    while IFS= read -r f; do
      [ -n "$f" ] && [ -f "$f" ] && files+=("$f")
    done < <({ git diff --name-only --diff-filter=AR "$merge_base" -- "$MIGRATIONS/*.sql"; git ls-files --others --exclude-standard -- "$MIGRATIONS/*.sql"; } | sort -u)
    ;;
esac

if [ "${#files[@]}" -eq 0 ]; then
  echo "lint-migrations: no migration added; nothing to lint"
  exit 0
fi
echo "lint-migrations: ${files[*]}"
lint_files "${files[@]}"
