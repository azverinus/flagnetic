#!/usr/bin/env bash
# Lints the migrations a change adds with squawk, so that a replica of the
# previous minor keeps working against the schema the new one migrates to
# (expand/contract: add, never rename or drop what the previous minor uses, and
# take no lock that stops it for long). Settings are in .squawk.toml.
#
#   lint-migrations.sh --base REF   the migrations added, renamed or modified since
#                                   the merge base with REF, but not the released
#                                   ones (those pinned in released.sum)
#   lint-migrations.sh FILE...      those files
#   lint-migrations.sh --self-test  each deliberately bad migration under
#                                   .github/scripts/testdata/migration-lint must fail
#
# A migration that has not been released is linted again whenever it changes:
# a later pull request can edit one an earlier pull request added and passed.
# A released one is never linted: it is pinned in released.sum, where a test
# fails on any edit, and it was written before the rule. An exception is a squawk
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

# changed_migrations <repository> <merge base>: the migration files added,
# renamed or modified since the merge base, committed or not, that released.sum
# does not pin, one per line.
changed_migrations() {
  local repo="$1" merge_base="$2" f
  while IFS= read -r f; do
    [ -n "$f" ] && [ -f "$repo/$f" ] || continue
    awk -v f="$(basename "$f")" '$2 == f { found = 1 } END { exit !found }' "$repo/$MIGRATIONS/released.sum" && continue
    echo "$f"
  done < <({
    git -C "$repo" diff --name-only --diff-filter=ARM "$merge_base" -- "$MIGRATIONS/*.sql"
    git -C "$repo" ls-files --others --exclude-standard -- "$MIGRATIONS/*.sql"
  } | sort -u)
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
  rc=$(( rc | $(changed_migrations_self_test) ))
  return $rc
}

# The files a change gets linted for: an added one, a modified one that is not
# released yet, and not one pinned in released.sum.
changed_migrations_self_test() {
  local dir rc=0 git=(git -c user.name=t -c user.email=t@example.com -c commit.gpgsign=false) got
  dir="$(mktemp -d)" || { echo 1; return; }
  mkdir -p "$dir/$MIGRATIONS"
  git -C "$dir" init -q
  for f in 00001_released 00002_unreleased 00003_untouched; do echo "-- +goose Up" >"$dir/$MIGRATIONS/$f.sql"; done
  echo "$(printf '0%.0s' {1..64})  00001_released.sql" >"$dir/$MIGRATIONS/released.sum"
  git -C "$dir" add -A && "${git[@]}" -C "$dir" commit -q -m base
  echo "ALTER TABLE flags DROP COLUMN owner;" >>"$dir/$MIGRATIONS/00001_released.sql"
  echo "ALTER TABLE flags DROP COLUMN owner;" >>"$dir/$MIGRATIONS/00002_unreleased.sql"
  echo "-- +goose Up" >"$dir/$MIGRATIONS/00004_added.sql"
  got="$(changed_migrations "$dir" HEAD | xargs -n1 basename 2>/dev/null | tr '\n' ' ')"
  if [ "$got" != "00002_unreleased.sql 00004_added.sql " ]; then
    echo "lint-migrations: a change to a released, an unreleased and a new migration selects [$got], want [00002_unreleased.sql 00004_added.sql ]" >&2
    rc=1
  fi
  rm -rf "$dir"
  echo $rc
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
    while IFS= read -r f; do
      [ -n "$f" ] && files+=("$f")
    done < <(changed_migrations "$root" "$merge_base")
    ;;
esac

if [ "${#files[@]}" -eq 0 ]; then
  echo "lint-migrations: no unreleased migration added or changed; nothing to lint"
  exit 0
fi
echo "lint-migrations: ${files[*]}"
lint_files "${files[@]}"
