#!/usr/bin/env bash
# Tests the rules of ci-plan.sh: it sources the plan with a fixed list of
# changed files and checks which test packages, modules and jobs follow. A
# rule that leaves a check out for a path it reads is found here, on the pull
# request that changes the plan or the tests, and not by the nightly run.
#
#   .github/scripts/ci-plan-test.sh
#
# Needs bash 3.2 (macOS) or later, git, go, jq, awk.
set -uo pipefail

cd "$(git rev-parse --show-toplevel)" || exit 2
# shellcheck source=.github/scripts/ci-plan.sh
. .github/scripts/ci-plan.sh

FAILURES=0
CASE=""

fail() {
  FAILURES=$((FAILURES + 1))
  echo "FAIL $CASE: $*" >&2
}

# plan_of <file...>: the plan of a pull request that changes exactly these.
plan_of() {
  CASE="$*"
  PLAN_MODE="pr"
  CHANGED="$(printf '%s\n' "$@")"
  plan_compute
}

tests_package() { grep -qx "$ROOT_MODULE/$1" <<<"$TEST_PKGS"; }
runs_module() { grep -qx "$1" <<<"$MODULES"; }

expect_tests() { # <package...>: planned for the server's tests
  local p
  for p in "$@"; do tests_package "$p" || fail "the tests of $p are not planned"; done
}
expect_module() { runs_module "$1" || fail "the module $1 is not planned"; }
expect_no_module() { ! runs_module "$1" || fail "the module $1 is planned"; }
expect_on() { # <variable>
  [ "${!1}" = 1 ] || fail "$1 is not set"
}
expect_off() { # <variable>
  [ "${!1}" = 0 ] || fail "$1 is set"
}

# The tests that read a page through the test kit run for a change to it.
plan_of docs/exported-events.md
expect_tests internal/infra/out/sinks internal/infra/in/http/sdk
plan_of docs/sinks.md
expect_tests internal/infra/in/worker
plan_of docs/operations.md
expect_tests internal/infra/in/worker

# Every test that calls testkit.Doc is planned for a change to the docs, so a
# new one is covered without a rule of its own.
plan_of docs/glossary.md
while read -r f; do
  d="$(dirname "$f")"
  tests_package "${d#./}" || fail "$f reads a page through testkit.Doc and is not planned for a change to the docs"
done < <(grep -rlE "$GUARD_DOC_RE" --include='*_test.go' internal cmd)

# A change to no page runs none of them.
plan_of conformance/README.md
tests_package internal/infra/in/worker && fail "a change outside docs/ plans the tests that read a page"

# The Go SDK's job regenerates the Go column of the behaviour matrix.
plan_of docs/sdks/behaviour-matrix.md
expect_module sdks/go
plan_of .github/scripts/behaviour-matrix.sh
expect_module sdks/go
plan_of docs/glossary.md
expect_no_module sdks/go
expect_no_module eval

# Every script under .github/scripts is linted, and the scripts that decide
# what CI does are tested, by the workflows job.
plan_of .github/scripts/pr-check.sh
expect_on RUN_WORKFLOWS
plan_of .github/scripts/ci-plan-test.sh
expect_on RUN_WORKFLOWS
plan_of .github/scripts/check-title.sh
expect_on RUN_WORKFLOWS
plan_of docs/glossary.md
expect_off RUN_WORKFLOWS

# The end-to-end harness is linted where it changed.
plan_of e2e/go.mod
expect_on E2E_TOUCHED
plan_of docs/glossary.md
expect_off E2E_TOUCHED

# A pull request is tested on the oldest PostgreSQL, the one pr-check starts.
CASE="postgres"
[ "$POSTGRES_OLDEST" = "[$(postgres_oldest)]" ] || fail "the oldest PostgreSQL is $(postgres_oldest), the pull request plan has $POSTGRES_OLDEST"

if [ "$FAILURES" -gt 0 ]; then
  echo "ci-plan-test: $FAILURES failed" >&2
  exit 1
fi
echo "ok: the plan follows its rules"
