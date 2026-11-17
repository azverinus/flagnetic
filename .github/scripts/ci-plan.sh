#!/usr/bin/env bash
# Decides which checks a change needs. ci.yml runs it in its `plan` job and
# pr-check.sh sources it, so what runs in CI and what runs on a developer's
# machine follow from the same rules and cannot drift.
#
#   ci-plan.sh --mode pr   --base REF [--head-ref BRANCH]
#   ci-plan.sh --mode main --base REF
#   ci-plan.sh --mode full
#
# It prints key=value lines, the outputs of the `plan` job.
#
#   pr    what the files changed since the merge base with REF can break:
#         a check runs only when a path it depends on changed. The server's
#         tests are those of the changed packages, of the packages that depend
#         on them and of the guard tests, on the oldest supported PostgreSQL.
#   main  the same for the files changed since REF, the last commit of main
#         whose run passed, except that a change to the server or the console
#         runs everything: a pull request tests such a change against its own
#         paths only, so main is where it meets the SDKs, the end-to-end
#         suites and the newest PostgreSQL.
#   full  everything: the nightly run, the server's release pull request, a
#         change to this script or to ci.yml, and a diff that cannot be read.
#
# Needs bash 3.2 (macOS) or later, git, go, jq, awk.
set -uo pipefail

# ------------------------------------------------------------ path rules
#
# Extended regular expressions over the changed paths, relative to the root.

# The rules themselves: a change to them is tested by running everything.
RE_FULL='^\.github/(workflows/ci\.yml|scripts/ci-plan\.sh)$'
# What every Go check reads: the lint and coverage settings, the workspace and
# the scripts the Go jobs run. They run for the server and every module.
RE_GO_ALL='^\.(golangci|testcoverage)\.yml$|^go\.work|^\.github/(coverage-floors/|scripts/(check-tidy|check-coverage|check-released-requirements|coverage-by-layer)\.sh$)'
# Tidy, generated code and govulncheck: Go sources, module files, and the API
# documents the generated code comes from.
RE_GO='\.go$|(^|/)go\.(mod|sum)$|^go\.work|^api/'
# The server's lint and the tests that parse its source. The documentation is
# not here: only the tests that read a page can be broken by it, and those
# are found as guard tests below.
RE_SERVER='^(internal|cmd|api|conformance|deploy)/|^go\.(mod|sum|work)|^web/apps/console/'
RE_CONSOLE='^web/|^api/management\.yaml$|^internal/infra/in/http/console/'
# The console end to end: the console, its handlers and the suite. The
# generated client is left out, as it changes with every edit of the
# management API.
RE_CONSOLE_E2E='^(web/apps/|web/packages/|web/e2e/|web/pnpm-lock\.yaml$|internal/infra/in/http/console/)'
RE_CONSOLE_E2E_NOT='^web/packages/api/src/generated/'
# Each SDK: its sources, the conformance suite its evaluator must pass, the
# SDK contract, the Go release its Unicode tables come from, the pages its
# tests compare with, and the scripts and the workflow that run it.
RE_SDK_JS='^(sdks/js/|conformance/|api/sdk\.yaml$|go\.mod$|docs/sdks/behaviour-matrix\.md$|\.github/scripts/(behaviour-matrix|playwright-version)\.sh$|\.github/workflows/sdk-js\.yml$)'
RE_SDK_PYTHON='^(sdks/python/|conformance/|api/sdk\.yaml$|go\.mod$|docs/sdks/(behaviour-matrix|compatibility)\.md$|\.github/scripts/behaviour-matrix\.sh$|\.github/workflows/sdk-python\.yml$)'
RE_SDK_RUBY='^(sdks/ruby/|sdks/ruby-openfeature/|conformance/|api/sdk\.yaml$|go\.mod$|docs/sdks/(behaviour-matrix|compatibility)\.md$|\.github/scripts/(behaviour-matrix|check-gem)\.sh$|\.github/workflows/sdk-ruby\.yml$)'
RE_DART='^(sdks/dart/|conformance/|api/sdk\.yaml$|docs/sdks/(behaviour-matrix|compatibility)\.md$|\.github/scripts/(behaviour-matrix|check-pub-package)\.sh$|\.github/workflows/sdk-dart\.yml$)'
# The Flutter package resolves the Dart SDK from the working tree, so a
# change to either one tests it.
RE_FLUTTER='^(sdks/dart/|sdks/flutter/|\.github/scripts/check-pub-package\.sh$|\.github/workflows/sdk-flutter\.yml$)'
# What every published Go module is tested with besides its own sources.
RE_MODULES='^(conformance/|api/sdk\.yaml$)'
RE_WORKFLOWS='^\.github/(workflows/|actionlint\.yaml$|scripts/check-ci-ok\.sh$)'

# Merging the server's release pull request publishes the image, so it is
# the last point where everything is checked together.
SERVER_RELEASE_BRANCH='release-please--branches--main--components--server'

# The oldest and the newest supported PostgreSQL. A pull request tests the
# oldest, where a feature newer than the minimum fails.
POSTGRES_OLDEST='[16]'
POSTGRES_ALL='[16,18]'

# The server's test packages that take minutes each under the race detector.
# They get test jobs of their own; the rest share one. The first is too long
# for one job even alone, and about half of its tests are named TestA...,
# so that prefix splits it in two.
SPLIT_PACKAGE='internal/infra/in/http/management'
SPLIT_PREFIX='^TestA'
GROUPED_PACKAGES='internal/infra/in/http/sdk internal/infra/in/http/sso'

# A guard test reads files outside its own package, so a change elsewhere can
# break it. Two kinds, told apart by what they read:
#   - a test that parses Go source (go/parser, go/ast, go/packages) checks
#     every server package, so it runs whenever the server changed;
#   - a test that opens a fixed file elsewhere in the tree ("../../api/x.yaml")
#     can only be broken by a change under that top-level name, so it runs
#     when something there changed.
GUARD_SOURCE_RE='go/parser|go/packages|go/ast|parser\.Parse|packages\.Load'
GUARD_PATH_RE='"(\.\./)+[^"]+"'
GENERATED_RE='\.gen\.go$'

# ------------------------------------------------------------ the diff

# plan_changed <commit>: the files that differ from it, untracked ones included.
plan_changed() {
  CHANGED="$( { git diff --name-only --no-renames "$1"; git ls-files --others --exclude-standard; } | sort -u | sed '/^$/d')"
}

changed() { grep -qE "$1" <<<"$CHANGED"; }

# A plan that could not be worked out must stop the run: a half-made one
# would name fewer checks than the change needs, and they would pass unrun.
plan_fail() { echo "ci-plan: $*" >&2; exit 1; }

# module_of <file or directory relative to the root>: index of the deepest
# module containing it.
module_of() {
  local best=-1 bestlen=-1 i rel len
  for i in "${!MOD_REL[@]}"; do
    rel="${MOD_REL[$i]}"
    if [ "$rel" = "." ]; then len=0
    elif [ "$1" = "$rel" ] || [ "${1#"$rel"/}" != "$1" ]; then len="${#rel}"
    else continue; fi
    if [ "$len" -gt "$bestlen" ]; then best="$i"; bestlen="$len"; fi
  done
  printf '%s' "$best"
}

# import_path <directory relative to the root>
import_path() {
  local m rel
  m="$(module_of "$1")"
  rel="${MOD_REL[$m]}"
  if [ "$rel" = "." ]; then
    if [ "$1" = "." ]; then printf '%s' "${MOD_PATH[$m]}"; else printf '%s/%s' "${MOD_PATH[$m]}" "$1"; fi
  elif [ "$1" = "$rel" ]; then printf '%s' "${MOD_PATH[$m]}"
  else printf '%s/%s' "${MOD_PATH[$m]}" "${1#"$rel"/}"; fi
}

# The package directory of a changed file: its own, or for a file a package
# embeds (a migration, a template), the nearest directory above holding Go.
package_dir() {
  local d
  d="$(dirname "$1")"
  while [ "$d" != "." ] && ! ls "$d"/*.go >/dev/null 2>&1; do d="$(dirname "$d")"; done
  printf '%s' "$d"
}

guard_packages() {
  local f top
  if [ "$SERVER" = 1 ]; then
    grep -rlE "$GUARD_SOURCE_RE" --include='*_test.go' internal cmd 2>/dev/null
  fi
  grep -rlE "$GUARD_PATH_RE" --include='*_test.go' internal cmd 2>/dev/null | while read -r f; do
    for top in $(grep -ohE "$GUARD_PATH_RE" "$f" | tr -d '"' | sed -E 's#^(\.\./)+##' | cut -d/ -f1 | sort -u); do
      if changed "^$top(/|\$)"; then echo "$f"; break; fi
    done
  done
}

# ------------------------------------------------------------ the plan

# plan_compute: reads PLAN_MODE (pr, main or full) and CHANGED, and sets what
# the plan consists of. Run it from the root of the repository.
plan_compute() {
  local root path dir rel i j f m d ip
  root="$(git rev-parse --show-toplevel)"
  FULL=0
  [ "$PLAN_MODE" = full ] && FULL=1
  changed "$RE_FULL" && FULL=1

  # The Go modules of go.work: path, directory relative to the repository.
  MOD_PATH=(); MOD_REL=(); MOD_TOUCHED=(); MOD_RUN=()
  while IFS='|' read -r path dir; do
    [ -n "$path" ] || continue
    MOD_PATH+=("$path")
    rel="${dir#"$root"}"; rel="${rel#/}"; [ -n "$rel" ] || rel="."
    MOD_REL+=("$rel")
    MOD_TOUCHED+=(0)
    MOD_RUN+=(0)
  done < <(go list -m -f '{{.Path}}|{{.Dir}}')
  [ "${#MOD_PATH[@]}" -gt 0 ] || plan_fail "go list -m names no module of go.work"
  ROOT_MODULE="${MOD_PATH[0]}"
  for i in "${!MOD_PATH[@]}"; do [ "${MOD_REL[$i]}" = "." ] && ROOT_MODULE="${MOD_PATH[$i]}"; done

  CHANGED_PKGS=""      # import paths of the packages with a change
  CHANGED_ROOT_DIRS="" # their directories, root module only
  WIDE=0               # every package of the server is tested
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    m="$(module_of "$f")"
    MOD_TOUCHED[$m]=1
    case "$f" in
      go.mod|go.sum|go.work) WIDE=1 ;;
    esac
    case "$f" in
      *.go) d="$(dirname "$f")" ;;
      internal/*|cmd/*|eval/*|sdks/go/*|sdks/go-openfeature/*) case "$f" in *.md) continue ;; esac; d="$(package_dir "$f")" ;;
      *) continue ;;
    esac
    ip="$(import_path "$d")"
    CHANGED_PKGS="$CHANGED_PKGS$ip"$'\n'
    [ "${MOD_REL[$m]}" = "." ] && CHANGED_ROOT_DIRS="$CHANGED_ROOT_DIRS$d"$'\n'
  done <<EOF
$CHANGED
EOF
  CHANGED_PKGS="$(printf '%s' "$CHANGED_PKGS" | sort -u | sed '/^$/d')"
  CHANGED_ROOT_DIRS="$(printf '%s' "$CHANGED_ROOT_DIRS" | sort -u | sed '/^$/d')"

  ANY_GO=0; SERVER=0
  RUN_CONSOLE=0; RUN_CONSOLE_E2E=0; RUN_SDK_JS=0; RUN_PYTHON=0; RUN_RUBY=0; RUN_DART=0; RUN_FLUTTER=0; RUN_WORKFLOWS=0
  changed "$RE_GO" && ANY_GO=1
  changed "$RE_SERVER" && SERVER=1
  [ -n "$CHANGED_ROOT_DIRS" ] && SERVER=1
  if changed "$RE_GO_ALL"; then ANY_GO=1; SERVER=1; WIDE=1; fi
  changed "$RE_CONSOLE" && RUN_CONSOLE=1
  grep -vE "$RE_CONSOLE_E2E_NOT" <<<"$CHANGED" | grep -qE "$RE_CONSOLE_E2E" && RUN_CONSOLE_E2E=1
  changed "$RE_SDK_JS" && RUN_SDK_JS=1
  changed "$RE_SDK_PYTHON" && RUN_PYTHON=1
  changed "$RE_SDK_RUBY" && RUN_RUBY=1
  changed "$RE_DART" && RUN_DART=1
  changed "$RE_FLUTTER" && RUN_FLUTTER=1
  changed "$RE_WORKFLOWS" && RUN_WORKFLOWS=1

  # The server's packages that can see a change: those that changed and those
  # whose tests depend on one, in the server or in a module it imports (their
  # tests start the server through the test kit). When the dependencies
  # cannot be listed, every package is tested instead of a guess.
  DEP_PKGS=""
  if [ "$FULL" = 0 ] && [ "$WIDE" = 0 ] && [ -n "$CHANGED_PKGS" ]; then
    if d="$(go list -test -f '{{.ImportPath}} {{join .Deps " "}}' ./... 2>/dev/null)"; then
      DEP_PKGS="$(awk '
        NR == FNR { changed[$1] = 1; next }
        $1 ~ /\.test$/ {
          pkg = $1; sub(/\.test$/, "", pkg)
          hit = (pkg in changed)
          for (i = 2; i <= NF && !hit; i++) if ($i in changed) hit = 1
          if (hit) print pkg
        }' <(printf '%s\n' "$CHANGED_PKGS") <(printf '%s\n' "$d") | grep -E "^$ROOT_MODULE(/|$)" | sort -u)"
      [ -n "$DEP_PKGS" ] && SERVER=1
    else
      echo "ci-plan: go list could not resolve the server's packages; testing all of them" >&2
      SERVER=1; WIDE=1
    fi
  fi

  if [ "$PLAN_MODE" = main ] && { [ "$SERVER" = 1 ] || [ "$RUN_CONSOLE" = 1 ]; }; then FULL=1; fi
  if [ "$FULL" = 1 ]; then
    ANY_GO=1; SERVER=1; WIDE=1
    RUN_CONSOLE=1; RUN_CONSOLE_E2E=1; RUN_SDK_JS=1; RUN_PYTHON=1; RUN_RUBY=1; RUN_DART=1; RUN_FLUTTER=1; RUN_WORKFLOWS=1
  fi

  # The published Go modules (the end-to-end harness is published nowhere and
  # has a job of its own): affected when they, or a module they require,
  # changed, or the conformance suite or the SDK contract they are tested with.
  for i in "${!MOD_PATH[@]}"; do
    [ "${MOD_REL[$i]}" = "." ] && continue
    [ "${MOD_REL[$i]}" = "e2e" ] && continue
    [ "${MOD_TOUCHED[$i]}" = 1 ] && MOD_RUN[$i]=1
    if [ "$FULL" = 1 ] || changed "$RE_MODULES" || changed "$RE_GO_ALL"; then MOD_RUN[$i]=1; fi
  done
  for _ in 1 2; do # a module that requires an affected module is affected
    for i in "${!MOD_PATH[@]}"; do
      [ "${MOD_REL[$i]}" = "." ] && continue
      [ "${MOD_REL[$i]}" = "e2e" ] && continue
      [ "${MOD_RUN[$i]}" = 1 ] && continue
      for j in "${!MOD_PATH[@]}"; do
        [ "${MOD_RUN[$j]}" = 1 ] || continue
        if go mod edit -json "${MOD_REL[$i]}/go.mod" | jq -e --arg p "${MOD_PATH[$j]}" '.Require[]? | select(.Path == $p)' >/dev/null; then MOD_RUN[$i]=1; fi
      done
    done
  done
  MODULES=""; E2E_TOUCHED=0
  for i in "${!MOD_PATH[@]}"; do
    [ "${MOD_RUN[$i]}" = 1 ] && MODULES="$MODULES${MOD_REL[$i]}"$'\n'
    [ "${MOD_REL[$i]}" = "e2e" ] && [ "${MOD_TOUCHED[$i]}" = 1 ] && E2E_TOUCHED=1
  done
  MODULES="$(printf '%s' "$MODULES" | sed '/^$/d')"

  # The server's tests and the packages whose coverage they are held to.
  TEST_PKGS=""; COVER_PKGS=""
  if [ "$WIDE" = 1 ]; then
    TEST_PKGS="$(go list ./...)" && [ -n "$TEST_PKGS" ] || plan_fail "go list ./... names no package of the server"
    COVER_PKGS="./internal/..."
  else
    GUARDS="$(guard_packages | while read -r f; do import_path "$(dirname "$f")"; echo; done | sed '/^$/d')"
    TEST_PKGS="$(printf '%s\n%s\n' "$DEP_PKGS" "$GUARDS" | sort -u | sed '/^$/d' | grep -E "^$ROOT_MODULE(/|$)")"
    if [ -n "$TEST_PKGS" ]; then
      # A changed package without tests of its own is covered by the tests of
      # the packages that depend on it.
      COVER_PKGS="$(printf '%s\n' "$CHANGED_PKGS" | grep -E "^$ROOT_MODULE/internal/" | grep -vE "$GENERATED_RE" | sed "s#^$ROOT_MODULE#.#" | while read -r d; do ls "$d"/*.go 2>/dev/null | grep -qv '_test\.go$' && echo "$d"; done | tr '\n' ',' | sed 's/,$//')"
    fi
  fi

  # The SDK end to end runs the real binary and the Go SDK against it, and
  # the smoke of each other SDK whose paths changed.
  RUN_SDK_E2E=0
  if [ "$SERVER" = 1 ] || [ -n "$MODULES" ] || [ "$E2E_TOUCHED" = 1 ] ||
    [ "$RUN_SDK_JS$RUN_PYTHON$RUN_RUBY$RUN_DART" != "0000" ]; then RUN_SDK_E2E=1; fi
  # The image holds the server and the console built into it.
  RUN_DOCKER=0
  if [ "$SERVER" = 1 ] || [ "$RUN_CONSOLE" = 1 ]; then RUN_DOCKER=1; fi
}

# ------------------------------------------------------------ the output

bool() { if [ "$1" = 1 ]; then echo true; else echo false; fi; }

shard() { # <id> <flags> <packages...>
  local id="$1" flags="$2"
  shift 2
  [ $# -gt 0 ] || return 0
  jq -nc --arg id "$id" --arg flags "$flags" --arg packages "$*" '{id: $id, flags: $flags, packages: $packages}'
}

# plan_shards: the test jobs of the server as a JSON list, each with the
# packages it tests and the go test flags that select its part of them.
plan_shards() {
  local p rel split="" grouped="" rest="" g hit
  for p in $TEST_PKGS; do
    rel="${p#"$ROOT_MODULE"}"; rel="${rel#/}"
    hit=0
    for g in $GROUPED_PACKAGES; do [ "$rel" = "$g" ] && hit=1; done
    if [ "$rel" = "$SPLIT_PACKAGE" ]; then split="./$rel"
    elif [ "$hit" = 1 ]; then grouped="$grouped ./$rel"
    else rest="$rest ./${rel:-.}"; fi
  done
  {
    # -run and -skip of one pattern: every test is in exactly one of the two.
    # shellcheck disable=SC2086
    shard management-1 "-run $SPLIT_PREFIX" $split
    # shellcheck disable=SC2086
    shard management-2 "-skip $SPLIT_PREFIX" $split
    # shellcheck disable=SC2086
    shard sdk-sso "" $grouped
    # shellcheck disable=SC2086
    shard rest "" $rest
  } | jq -sc .
}

plan_print() {
  local modules shards
  modules="$(printf '%s\n' "$MODULES" | jq -Rsc 'split("\n") | map(select(. != ""))')"
  shards="$(plan_shards)"
  case "$modules$shards" in
    \[*\]\[*\]) ;;
    *) plan_fail "jq did not write the lists of modules and test jobs" ;;
  esac
  echo "full=$(bool "$FULL")"
  echo "go=$(bool "$ANY_GO")"
  echo "server=$(bool "$SERVER")"
  echo "console=$(bool "$RUN_CONSOLE")"
  echo "console_e2e=$(bool "$RUN_CONSOLE_E2E")"
  echo "docker=$(bool "$RUN_DOCKER")"
  echo "sdk_e2e=$(bool "$RUN_SDK_E2E")"
  echo "sdk_js=$(bool "$RUN_SDK_JS")"
  echo "sdk_python=$(bool "$RUN_PYTHON")"
  echo "sdk_ruby=$(bool "$RUN_RUBY")"
  echo "dart=$(bool "$RUN_DART")"
  echo "flutter=$(bool "$RUN_FLUTTER")"
  echo "workflows=$(bool "$RUN_WORKFLOWS")"
  echo "modules=$modules"
  echo "test_shards=$shards"
  echo "cover_packages=$COVER_PKGS"
  echo "postgres=$POSTGRES"
}

main() {
  local base="" head_ref="" merge_base
  PLAN_MODE=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --mode) shift; PLAN_MODE="${1:?--mode needs pr, main or full}" ;;
      --base) shift; base="${1:?--base needs a ref}" ;;
      --head-ref) shift; head_ref="${1-}" ;;
      *) echo "ci-plan: unknown argument $1" >&2; exit 2 ;;
    esac
    shift
  done
  case "$PLAN_MODE" in
    pr|main) [ -n "$base" ] || { echo "ci-plan: --mode $PLAN_MODE needs --base" >&2; exit 2; } ;;
    full) ;;
    *) echo "ci-plan: --mode must be pr, main or full" >&2; exit 2 ;;
  esac
  cd "$(git rev-parse --show-toplevel)" || exit 2

  POSTGRES="$POSTGRES_ALL"
  [ "$PLAN_MODE" = pr ] && POSTGRES="$POSTGRES_OLDEST"
  if [ "$head_ref" = "$SERVER_RELEASE_BRANCH" ]; then PLAN_MODE=full; POSTGRES="$POSTGRES_ALL"; fi

  CHANGED=""
  if [ "$PLAN_MODE" != full ]; then
    if merge_base="$(git merge-base HEAD "$base" 2>/dev/null)"; then
      plan_changed "$merge_base"
      # Nothing to tell the checks apart by, so all of them run.
      [ -n "$CHANGED" ] || PLAN_MODE=full
    else
      echo "ci-plan: no merge base with $base; running everything" >&2
      PLAN_MODE=full
    fi
  fi
  plan_compute
  plan_print
}

# Sourced by pr-check.sh for the functions above; run for the plan itself.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then main "$@"; fi
