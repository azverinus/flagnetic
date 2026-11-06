#!/usr/bin/env bash
# Runs on this machine the checks CI will run for the changes of the current
# branch, so a pull request is opened green. Run it through `task pr:check`,
# which passes the pinned versions of the tools.
#
#   task pr:check                  the checks for the branch against origin/main
#   task pr:check -- --plan        show what would run, run nothing
#   task pr:check -- --base REF    compare with another base than origin/main
#
# What runs follows from the files changed since the merge base with
# origin/main, committed or not, and mirrors the jobs of ci.yml:
#
#   always            DCO sign-off of the commits, and the workflows (actionlint)
#                     when one changed
#   Go changed        go.mod tidy of every module of go.work, generated code
#                     current, govulncheck
#   server changed    build, golangci-lint, then the tests of the changed
#                     packages, of every package that depends on them (their
#                     tests exercise them through the test kit) and of the
#                     guard tests, with the race detector and a coverage profile
#                     that go-test-coverage checks against .testcoverage.yml
#   a Go module       lint, tests with its coverage floors, when it or a module
#                     it requires changed
#   console, the JavaScript, Python, Ruby, Dart and Flutter SDKs
#                     the steps of their jobs, when their paths changed
#                     (the path filters are read from the `what changed` jobs
#                     of ci.yml, so the two cannot drift)
#
# Not replicated, left to CI: CodeQL, the docker build, the oldest Go release
# of the module matrix, the other PostgreSQL versions (tests run on one), the
# browsers of the SDK suites, the released-requirements check, the
# end-to-end suites, and Playwright beyond the specs that changed.
#
# The heavy steps (lint, tests, builds) run under a lock shared by every
# checkout on the machine, so several sessions queue instead of heating it
# together. Settings, all optional:
#
#   PR_CHECK_P          packages built and tested at once (go test -p, lint
#                       concurrency); default 4
#   PR_CHECK_PARALLEL   tests of one package run at once (go test -parallel);
#                       default 4
#   PR_CHECK_NICE       niceness of the heavy steps; default 10
#   PR_CHECK_LOCK       the lock; default under ~/.local/state/flagnetic
#   PR_CHECK_POSTGRES   PostgreSQL major version of the shared test database
#                       container; default 17. DATABASE_URL, when set, is used
#                       instead of the container.
#   PR_CHECK_PG_PORT    its host port; default 54329
#   PR_CHECK_TEST_FLAGS extra flags for the server's go test (-run, -skip), for
#                       diagnosing one failure. A run with them is not the
#                       run CI does, and says so at the end; never use them to
#                       get past a failure.
#
# Needs bash 3.2 (macOS) or later, git, go, jq, awk; docker for the tests.
set -uo pipefail

: "${GOLANGCI_LINT:?run this through task pr:check}"
: "${GOVULNCHECK:?run this through task pr:check}"
: "${ACTIONLINT:?run this through task pr:check}"
: "${GO_TEST_COVERAGE:?run this through task pr:check}"

P="${PR_CHECK_P:-4}"
PARALLEL="${PR_CHECK_PARALLEL:-4}"
NICE="${PR_CHECK_NICE:-10}"
LOCK="${PR_CHECK_LOCK:-${XDG_STATE_HOME:-$HOME/.local/state}/flagnetic/pr-check.lock}"
PG_VERSION="${PR_CHECK_POSTGRES:-17}"
PG_PORT="${PR_CHECK_PG_PORT:-54329}"
PG_CONTAINER="flagnetic-pr-check-postgres"
TEST_FLAGS="${PR_CHECK_TEST_FLAGS:-}"

PLAN_ONLY=0
BASE_REF="origin/main"
while [ $# -gt 0 ]; do
  case "$1" in
    --plan) PLAN_ONLY=1 ;;
    --base) shift; BASE_REF="${1:?--base needs a ref}" ;;
    *) echo "pr-check: unknown argument $1" >&2; exit 2 ;;
  esac
  shift
done

ROOT="$(git rev-parse --show-toplevel)" || exit 2
cd "$ROOT" || exit 2
WORK="$(mktemp -d "${TMPDIR:-/tmp}/pr-check.XXXXXX")"
RESULTS="$WORK/results.tsv"
: > "$RESULTS"
FAILED=0
LOCK_TOKEN=""

now() { date +%s; }
duration() { printf '%dm%02ds' $(($1 / 60)) $(($1 % 60)); }
say() { printf '%s\n' "$*"; }

cleanup() {
  local rc=$?
  lock_release
  [ -d "$WORK/base-tree" ] && git worktree remove --force "$WORK/base-tree" >/dev/null 2>&1
  jobs -p 2>/dev/null | while read -r pid; do kill "$pid" 2>/dev/null; done
  if [ "$FAILED" -eq 0 ] && [ "$rc" -eq 0 ]; then rm -rf "$WORK"; fi
  return $rc
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# ---------------------------------------------------------------- the lock
#
# One lock for the machine, outside every checkout. It is a symlink whose
# target names the owner, because creating a symlink is atomic and carries its
# content with it (flock is not on macOS). An owner that is gone, or whose
# pid now belongs to another process, no longer holds it.

proc_start() { ps -o lstart= -p "$1" 2>/dev/null | sed 's/^ *//; s/  */ /g'; }

lock_owner_alive() { # $1 = lock target "pid|lstart|epoch|dir|branch"
  local pid started
  pid="${1%%|*}"
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null || return 1
  started="$(printf '%s' "$1" | cut -d'|' -f2)"
  [ "$(proc_start "$pid")" = "$started" ]
}

lock_acquire() {
  local target owner stale last=0 begun
  mkdir -p "$(dirname "$LOCK")"
  LOCK_TOKEN="$$|$(proc_start $$)|$(now)|$ROOT|$(git rev-parse --abbrev-ref HEAD 2>/dev/null)"
  begun="$(now)"
  while ! ln -s "$LOCK_TOKEN" "$LOCK" 2>/dev/null; do
    target="$(readlink "$LOCK" 2>/dev/null)" || continue
    if [ -n "$target" ] && ! lock_owner_alive "$target"; then
      # Take the dead owner's lock away. The rename succeeds for one
      # contender only; if what was moved is not what was judged dead, a
      # live owner took it in between, so it goes back.
      stale="$LOCK.stale.$$"
      if mv "$LOCK" "$stale" 2>/dev/null; then
        if [ "$(readlink "$stale")" = "$target" ]; then
          say "pr-check: the lock was left by a run that is gone (pid ${target%%|*}); taking it"
          rm -f "$stale"
        else
          mv "$stale" "$LOCK" 2>/dev/null || rm -f "$stale"
        fi
      fi
      continue
    fi
    if [ $(($(now) - last)) -ge 20 ]; then
      owner="$(printf '%s' "$target" | cut -d'|' -f4,5 | tr '|' ' ')"
      say "pr-check: waiting for the machine lock held by pid ${target%%|*} ($owner), running $(duration $(($(now) - $(printf '%s' "$target" | cut -d'|' -f3)))); waited $(duration $(($(now) - begun))) so far"
      last="$(now)"
    fi
    sleep 2
  done
  WAITED=$(($(now) - begun))
}

lock_release() {
  [ -n "$LOCK_TOKEN" ] || return 0
  if [ "$(readlink "$LOCK" 2>/dev/null)" = "$LOCK_TOKEN" ]; then rm -f "$LOCK"; fi
  LOCK_TOKEN=""
}

# ---------------------------------------------------------------- steps

record() { printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" >> "$RESULTS"; }

# step <name> <digest function> <what to do> <command...>
# Runs the command with its output in a log, and on failure prints the
# digest of the log and what to do about it.
step() {
  local name="$1" digest="$2" hint="$3" log start rc
  shift 3
  log="$WORK/$(printf '%s' "$name" | tr -c 'A-Za-z0-9' '_').log"
  say "▶ $name"
  start="$(now)"
  "$@" > "$log" 2>&1
  rc=$?
  if [ "$rc" -eq 0 ]; then
    record "$name" "ok" "$(($(now) - start))" ""
    return 0
  fi
  FAILED=1
  record "$name" "FAILED" "$(($(now) - start))" ""
  say "✗ $name failed"
  "$digest" "$log" | sed 's/^/    /'
  say "  → $hint"
  say "  full output: $log"
  return 1
}

skipped() { # <name> <why>
  record "$1" "skipped" 0 "$2"
}

not_run() { # <name> <why>: a check that could not run is not a pass
  record "$1" "NOT RUN" 0 "$2"
  say "! $1 was not run: $2"
}

digest_tail() { tail -n 15 "$1"; }
digest_gotest() {
  grep -E '^(--- FAIL|FAIL|panic:|\s+[A-Za-z0-9_./-]+_test\.go:[0-9]+:|\s+Error)' "$1" | head -n 25
  grep -q . "$1" && [ "$(grep -cE '^(--- FAIL|FAIL|panic:)' "$1")" -eq 0 ] && tail -n 12 "$1"
  return 0
}
digest_lint() { grep -E '^[A-Za-z0-9_./-]+\.go:[0-9]+' "$1" | head -n 25; grep -E '^[0-9]+ issues' "$1"; return 0; }
digest_diff() { head -n 30 "$1"; }
digest_vuln() { grep -E '^(Vulnerability|    Found in|    Fixed in|    Module|    More info)|^  (#[0-9]+)|^=== ' "$1" | head -n 30; return 0; }
digest_coverage() { grep -vE '^\s*$' "$1" | head -n 40; return 0; }

# ---------------------------------------------------------------- the diff

git fetch --quiet origin "${BASE_REF#origin/}" 2>/dev/null || say "pr-check: could not fetch $BASE_REF; using the copy of it you have"
BASE="$(git merge-base HEAD "$BASE_REF")" || { say "pr-check: no merge base with $BASE_REF"; exit 2; }
CHANGED="$( { git diff --name-only --no-renames "$BASE"; git ls-files --others --exclude-standard; } | sort -u | sed '/^$/d')"
UNCOMMITTED="$(git status --porcelain | wc -l | tr -d ' ')"

changed() { grep -qE "$1" <<<"$CHANGED"; }

if [ -z "$CHANGED" ]; then
  say "pr-check: no change since the merge base with $BASE_REF ($(git rev-parse --short "$BASE")); nothing to check"
  exit 0
fi

CI_YML=".github/workflows/ci.yml"
# The path filter of a "what changed" step of ci.yml: the <n>th `grep -qE`
# of step <id> in job <job>. The checks below run on the same paths CI does.
ci_filter() { # <job> <step id> <n>
  local re
  re="$(awk -v job="$1" -v id="$2" -v n="$3" '
    /^  [A-Za-z0-9_-]+:/ { injob = ($1 == job ":"); instep = 0; seen = 0 }
    injob && /^      - id:/ { instep = ($3 == id); seen = 0 }
    injob && instep && index($0, "grep -qE \047") {
      seen++
      if (seen == n) {
        s = substr($0, index($0, "grep -qE \047") + 10)
        print substr(s, 1, index(s, "\047") - 1)
        exit
      }
    }
  ' "$CI_YML")"
  if [ -z "$re" ]; then
    say "pr-check: cannot read the path filter $1/$2 #$3 from ci.yml; fix .github/scripts/pr-check.sh" >&2
    return 1
  fi
  printf '%s' "$re"
}

# The Go modules of go.work: path, directory relative to the repository.
MOD_PATH=(); MOD_REL=(); MOD_TOUCHED=()
while IFS='|' read -r path dir; do
  [ -n "$path" ] || continue
  MOD_PATH+=("$path")
  rel="${dir#"$ROOT"}"; rel="${rel#/}"; [ -n "$rel" ] || rel="."
  MOD_REL+=("$rel")
  MOD_TOUCHED+=(0)
done < <(go list -m -f '{{.Path}}|{{.Dir}}')
ROOT_MODULE="${MOD_PATH[0]}"
for i in "${!MOD_PATH[@]}"; do [ "${MOD_REL[$i]}" = "." ] && ROOT_MODULE="${MOD_PATH[$i]}"; done

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

CHANGED_PKGS=""      # import paths of the packages with a change
CHANGED_ROOT_DIRS="" # their directories, root module only
WIDE=0
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

ANY_GO=0
changed '\.go$|(^|/)go\.(mod|sum)$|^go\.work|^api/' && ANY_GO=1
# What the server's tests read besides Go: the API documents, the docs, the
# conformance fixtures, the console's sources.
SERVER=0
changed '^(internal|cmd|api|docs|conformance|deploy)/|^go\.(mod|sum|work)|^web/apps/console/|^\.testcoverage\.yml$' && SERVER=1
[ -n "$CHANGED_PKGS" ] && SERVER=1

GENERATED_RE='\.gen\.go$'
RUN_CONSOLE=0; RUN_SDK_JS=0; RUN_PYTHON=0; RUN_RUBY=0; RUN_DART=0; RUN_FLUTTER=0; RUN_WORKFLOWS=0
RE_SDK_JS="$(ci_filter changes sdk-js 1)" || exit 2
RE_PYTHON="$(ci_filter changes sdk-python 1)" || exit 2
RE_RUBY="$(ci_filter changes diff 2)" || exit 2
RE_DART="$(ci_filter dart-changes diff 1)" || exit 2
RE_FLUTTER="$(ci_filter dart-changes diff 2)" || exit 2
changed '^web/|^api/management\.yaml$|^internal/infra/in/http/console/' && RUN_CONSOLE=1
changed "$RE_SDK_JS" && RUN_SDK_JS=1
changed "$RE_PYTHON" && RUN_PYTHON=1
changed "$RE_RUBY" && RUN_RUBY=1
changed "$RE_DART" && RUN_DART=1
changed "$RE_FLUTTER" && RUN_FLUTTER=1
changed '^\.github/workflows/|^\.github/scripts/check-ci-ok\.sh$' && RUN_WORKFLOWS=1
SPECS="$(printf '%s\n' "$CHANGED" | grep -E '^web/e2e/.*\.spec\.ts$' | while read -r s; do [ -f "$s" ] && echo "${s#web/e2e/}"; done)"

# Go modules besides the root: affected when they, or a module they require,
# changed, or the conformance suite or the SDK contract they are tested with.
MOD_RUN=()
for i in "${!MOD_PATH[@]}"; do
  MOD_RUN[$i]=0
  [ "${MOD_REL[$i]}" = "." ] && continue
  [ "${MOD_REL[$i]}" = "e2e" ] && continue
  [ "${MOD_TOUCHED[$i]}" = 1 ] && MOD_RUN[$i]=1
  changed '^(conformance/|api/sdk\.yaml$)' && MOD_RUN[$i]=1
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

# ---------------------------------------------------------- the server tests
#
# The tests that can see a change: those of the packages that changed, those
# of every package importing one (their tests start the server through the
# test kit), and the guard tests, which read files outside their package.

# A guard test is one that reads files outside its own package, so a change
# elsewhere can break it. Two kinds, told apart by what they read:
#   - a test that parses Go source (go/parser, go/ast, go/packages) checks
#     every server package, so it runs whenever the server changed;
#   - a test that opens a fixed file elsewhere in the tree ("../../api/x.yaml")
#     can only be broken by a change under that top-level directory, so it
#     runs when something there changed. Running these on every server change
#     would add the largest test package to a change in a leaf package.
GUARD_SOURCE_RE='go/parser|go/packages|go/ast|parser\.Parse|packages\.Load'
GUARD_PATH_RE='"(\.\./)+[^"]+"'

guard_packages() {
  if [ "$SERVER" = 1 ]; then
    grep -rlE "$GUARD_SOURCE_RE" --include='*_test.go' internal cmd 2>/dev/null
  fi
  grep -rlE "$GUARD_PATH_RE" --include='*_test.go' internal cmd 2>/dev/null | while read -r f; do
    for top in $(grep -ohE "$GUARD_PATH_RE" "$f" | tr -d '"' | sed -E 's#^(\.\./)+##' | cut -d/ -f1 | sort -u); do
      if changed "^$top/"; then echo "$f"; break; fi
    done
  done
}

GUARDS="$(guard_packages | while read -r f; do import_path "$(dirname "$f")"; echo; done | sed '/^$/d' | sort -u)"

TEST_PKGS=""; COVER_PKGS=""
if [ "$SERVER" = 1 ] || [ -n "$CHANGED_PKGS" ] || [ -n "$GUARDS" ]; then
  if [ "$WIDE" = 1 ]; then
    TEST_PKGS="./..."
    COVER_PKGS="./internal/..."
  else
    go list -e -test -f '{{.ImportPath}} {{join .Deps " "}}' ./... > "$WORK/deps.txt" 2>/dev/null
    printf '%s\n' "$CHANGED_PKGS" > "$WORK/changed.txt"
    TEST_PKGS="$(awk '
      NR == FNR { changed[$1] = 1; next }
      $1 ~ /\.test$/ {
        pkg = $1; sub(/\.test$/, "", pkg)
        hit = (pkg in changed)
        for (i = 2; i <= NF && !hit; i++) if ($i in changed) hit = 1
        if (hit) print pkg
      }' "$WORK/changed.txt" "$WORK/deps.txt")"
    TEST_PKGS="$TEST_PKGS"$'\n'"$GUARDS"
    # A changed package without tests of its own is covered by the packages
    # above, but is built and vetted by the build step.
    TEST_PKGS="$(printf '%s\n' "$TEST_PKGS" | sort -u | sed '/^$/d' | grep -E "^$ROOT_MODULE(/|$)")"
    COVER_PKGS="$(printf '%s\n' "$CHANGED_PKGS" | grep -E "^$ROOT_MODULE/internal/" | grep -vE "$GENERATED_RE" | sed "s#^$ROOT_MODULE#.#" | while read -r d; do ls "$d"/*.go 2>/dev/null | grep -qv '_test\.go$' && echo "$d"; done | tr '\n' ',' | sed 's/,$//')"
  fi
fi

print_plan() {
  say "pr-check plan (base $(git rev-parse --short "$BASE") = merge base with $BASE_REF; $(printf '%s\n' "$CHANGED" | wc -l | tr -d ' ') files changed, $UNCOMMITTED uncommitted)"
  say "  Go changed: $ANY_GO   server: $SERVER   wide (go.mod/go.sum/go.work of the root): $WIDE"
  say "  changed packages: $(printf '%s' "$CHANGED_PKGS" | tr '\n' ' ')"
  say "  test packages:    $(printf '%s' "$TEST_PKGS" | sed "s#$ROOT_MODULE/##g" | tr '\n' ' ')"
  say "  coverage of:      $COVER_PKGS"
  for i in "${!MOD_PATH[@]}"; do [ "${MOD_RUN[$i]}" = 1 ] && say "  module checked:   ${MOD_REL[$i]}"; done
  say "  console: $RUN_CONSOLE  sdk-js: $RUN_SDK_JS  python: $RUN_PYTHON  ruby: $RUN_RUBY  dart: $RUN_DART  flutter: $RUN_FLUTTER  workflows: $RUN_WORKFLOWS  playwright specs: $(printf '%s' "$SPECS" | tr '\n' ' ')"
}
print_plan
[ "$PLAN_ONLY" = 1 ] && exit 0

# ---------------------------------------------------------------- checks

tree_fingerprint() { # one line per modified or untracked file with its content hash
  git ls-files -m -o --exclude-standard | while read -r f; do
    [ -f "$f" ] && printf '%s %s\n' "$(git hash-object "$f")" "$f"
  done
}

dco() { git log --format=%H "$BASE..HEAD" | grep -q . && .github/scripts/check-dco.sh "$BASE" HEAD || true; }

tidy_all() {
  local rc=0 i
  for i in "${!MOD_PATH[@]}"; do
    echo "== ${MOD_REL[$i]}"
    .github/scripts/check-tidy.sh "${MOD_REL[$i]}" || rc=1
  done
  return $rc
}

generated_current() {
  local before after
  before="$WORK/generate.before"; after="$WORK/generate.after"
  tree_fingerprint > "$before"
  go generate ./... || return 1
  tree_fingerprint > "$after"
  if ! diff -u "$before" "$after"; then
    echo "go generate changed the files above: the generated code was not current."
    return 1
  fi
}

build_root() { go build ./...; }

lint_module() { (cd "$1" && nice -n "$NICE" $GOLANGCI_LINT run --concurrency "$P" --config "$ROOT/.golangci.yml" ./...); }

ensure_postgres() {
  if [ -n "${DATABASE_URL:-}" ]; then return 0; fi
  command -v docker >/dev/null 2>&1 || { echo "docker is not installed: set DATABASE_URL to a PostgreSQL or install Docker"; return 1; }
  docker info >/dev/null 2>&1 || { echo "the Docker daemon is not running: start it, or set DATABASE_URL to a PostgreSQL"; return 1; }
  if ! docker ps -a --format '{{.Names}}' | grep -qx "$PG_CONTAINER"; then
    # One database for every run on the machine, in memory and without
    # fsync: each test package works in a schema of its own, as it does in
    # CI, so sharing it costs no isolation and saves a container per package.
    docker run -d --name "$PG_CONTAINER" --restart unless-stopped \
      -e POSTGRES_DB=flagnetic -e POSTGRES_USER=flagnetic -e POSTGRES_PASSWORD=flagnetic \
      -p "127.0.0.1:$PG_PORT:5432" --tmpfs /var/lib/postgresql/data \
      "postgres:$PG_VERSION-alpine" \
      -c fsync=off -c synchronous_commit=off -c full_page_writes=off -c max_connections=600 >/dev/null || return 1
  elif ! docker ps --format '{{.Names}}' | grep -qx "$PG_CONTAINER"; then
    docker start "$PG_CONTAINER" >/dev/null || return 1
  fi
  local n=0
  until docker exec "$PG_CONTAINER" pg_isready -U flagnetic -d flagnetic >/dev/null 2>&1; do
    n=$((n + 1)); [ "$n" -gt 120 ] && { echo "the test database did not become ready"; return 1; }
    sleep 0.5
  done
  export DATABASE_URL="postgres://flagnetic:flagnetic@127.0.0.1:$PG_PORT/flagnetic?sslmode=disable"
}

# The webhook tests deliver to https://receiver.example/..., a name that must
# not resolve. A resolver that answers every name (a proxy's fake-IP DNS) makes
# each of those deliveries wait out a timeout.
dns_answers_unknown_names() {
  local addr
  if command -v getent >/dev/null 2>&1; then addr="$(getent hosts receiver.example 2>/dev/null)"
  elif command -v dscacheutil >/dev/null 2>&1; then addr="$(dscacheutil -q host -a name receiver.example 2>/dev/null | grep -E '^ip(v6)?_address')"
  fi
  [ -z "${addr:-}" ] || { say "! this machine's DNS resolves receiver.example (${addr##* }): a proxy answering every name? The webhook delivery tests in internal/infra/in/http/management then wait ~60 s each and TestATestPingReachesTheReceiverSigned fails; CI's DNS refuses the name. Exclude *.example from the proxy's DNS to get a faithful run."; }
}

server_tests() {
  local args
  args="-race -count=1 -timeout 20m -p $P -parallel $PARALLEL"
  if [ -n "$COVER_PKGS" ]; then args="$args -coverpkg=$COVER_PKGS -coverprofile=$WORK/coverage.out"; fi
  # shellcheck disable=SC2086
  nice -n "$NICE" go test $args $TEST_FLAGS $TEST_PKGS
}

server_coverage() {
  [ -s "$WORK/coverage.out" ] || { echo "the tests wrote no coverage profile"; return 1; }
  sed "s#^profile:.*#profile: $WORK/coverage.out#" .testcoverage.yml > "$WORK/testcoverage.yml"
  $GO_TEST_COVERAGE --config="$WORK/testcoverage.yml"
}

module_tests() { # <dir>
  (cd "$1" && FLAGNETIC_SCENARIO_REPORT="$WORK/scenarios-$(basename "$1").json" nice -n "$NICE" go test -race -count=1 -p "$P" -parallel "$PARALLEL" -coverprofile="$WORK/coverage-$(basename "$1").out" ./...)
}
module_floors() { # <dir>
  .github/scripts/check-coverage.sh "$1" "$WORK/coverage-$(basename "$1").out" .github/coverage-floors | grep -E '❌|not in the profile' ; test "${PIPESTATUS[0]}" -eq 0
}
behaviour_matrix_go() {
  local before after
  before="$(shasum < docs/sdks/behaviour-matrix.md)"
  .github/scripts/behaviour-matrix.sh Go "$WORK/scenarios-go.json" || return 1
  after="$(shasum < docs/sdks/behaviour-matrix.md)"
  [ "$before" = "$after" ] || { echo "docs/sdks/behaviour-matrix.md is not what the Go SDK's run reports; commit the regenerated file"; return 1; }
}

# govulncheck of a module. A finding the merge base has as well is not
# caused by the change: it is said so, since it cannot be fixed within it.
vuln_module() { # <module dir>
  local out rc ids base_ids tree="$WORK/base-tree"
  out="$WORK/vuln-$(basename "$1").out"
  (cd "$1" && nice -n "$NICE" $GOVULNCHECK ./...) > "$out" 2>&1
  rc=$?
  cat "$out"
  [ "$rc" -eq 0 ] && return 0
  ids="$(grep -oE 'GO-[0-9]+-[0-9]+' "$out" | sort -u)"
  [ -n "$ids" ] || return "$rc"
  [ -d "$tree" ] || git worktree add --quiet --detach "$tree" "$BASE" >/dev/null 2>&1
  base_ids="$(cd "$tree/$1" 2>/dev/null && $GOVULNCHECK ./... 2>&1 | grep -oE 'GO-[0-9]+-[0-9]+' | sort -u)"
  echo
  for id in $ids; do
    if grep -qx "$id" <<<"$base_ids"; then
      echo "$id is also found at the merge base with $BASE_REF: NOT caused by this change. Do not fix it here; tell the maintainer, and rebase once main has the fix."
    else
      echo "$id is NOT found at the merge base: this change brings it in."
    fi
  done
  return "$rc"
}

# --- cheap checks, without the lock
if [ "$(git log --format=%H "$BASE..HEAD" | wc -l | tr -d ' ')" -gt 0 ]; then
  step "DCO sign-off" digest_tail "sign the commits: git rebase --signoff $BASE_REF" dco
fi
if [ "$ANY_GO" = 1 ]; then
  step "go.mod tidy, every module" digest_diff "run .github/scripts/check-tidy.sh <module> -w for each module listed, commit go.mod and go.sum" tidy_all
  step "generated code current" digest_diff "run task generate and commit what it changes" generated_current
fi
if [ "$RUN_WORKFLOWS" = 1 ]; then
  step "actionlint and ci-ok" digest_tail "fix the workflow; a new job of ci.yml goes into the needs of ci-ok" \
    bash -c "$ACTIONLINT && .github/scripts/check-ci-ok.sh"
fi

# --- heavy checks, one machine-wide queue
HEAVY=0
[ -n "$TEST_PKGS" ] && HEAVY=1
[ "$SERVER" = 1 ] && HEAVY=1
for i in "${!MOD_PATH[@]}"; do [ "${MOD_RUN[$i]}" = 1 ] && HEAVY=1; done
[ "$RUN_CONSOLE$RUN_SDK_JS$RUN_PYTHON$RUN_RUBY$RUN_DART$RUN_FLUTTER" != "000000" ] && HEAVY=1
WAITED=0
if [ "$HEAVY" = 1 ]; then
  lock_acquire
  [ "$WAITED" -gt 0 ] && record "waiting for other runs" "ok" "$WAITED" "not counted below"
fi

if [ "$SERVER" = 1 ]; then
  step "go build" digest_tail "fix the compile error" build_root
  step "golangci-lint (server)" digest_lint "fix the findings; the layer rules are not to be silenced" lint_module .
fi
for i in "${!MOD_PATH[@]}"; do
  if [ "${MOD_RUN[$i]}" = 1 ]; then
    step "golangci-lint (${MOD_REL[$i]})" digest_lint "fix the findings" lint_module "${MOD_REL[$i]}"
  fi
done

if [ -n "$TEST_PKGS" ]; then
  dns_answers_unknown_names
  if step "postgres for the tests" digest_tail "start Docker or set DATABASE_URL" ensure_postgres; then
        if [ "$TEST_PKGS" = "./..." ]; then test_label="every package"; else test_label="$(printf '%s\n' "$TEST_PKGS" | wc -l | tr -d ' ') packages"; fi
    step "go test -race ($test_label)" digest_gotest \
      "run the failing test alone: go test -race -run '<TestName>' <package>" server_tests
    if [ -n "$COVER_PKGS" ] && [ -s "$WORK/coverage.out" ]; then
      step "coverage floors" digest_coverage "add tests for the files listed (the floors are in .testcoverage.yml; do not lower them)" server_coverage
    fi
  fi
fi

for i in "${!MOD_PATH[@]}"; do
  [ "${MOD_RUN[$i]}" = 1 ] || continue
  dir="${MOD_REL[$i]}"
  step "go test -race ($dir)" digest_gotest "run the failing test alone in $dir" module_tests "$dir"
  [ -s "$WORK/coverage-$(basename "$dir").out" ] && step "coverage floors ($dir)" digest_coverage "raise the coverage of the packages listed (.github/coverage-floors)" module_floors "$dir"
  if [ "$dir" = "sdks/go" ]; then
    step "behaviour matrix (Go)" digest_tail "commit the regenerated docs/sdks/behaviour-matrix.md" behaviour_matrix_go
  fi
done

if [ "$ANY_GO" = 1 ]; then
  for i in "${!MOD_PATH[@]}"; do
    step "govulncheck (${MOD_REL[$i]})" digest_vuln "update the dependency named above; if origin/main has the same finding it is not from this change: tell the maintainer and wait for the fix on main" vuln_module "${MOD_REL[$i]}"
  done
fi

# --- the console and the SDKs
need_tool() { command -v "$1" >/dev/null 2>&1; }
js_step() { # <name> <dir> <command...>
  local name="$1" dir="$2"; shift 2
  step "$name" digest_tail "fix what the output says" bash -c "cd '$dir' && $*"
}
if [ "$RUN_CONSOLE" = 1 ]; then
  if need_tool pnpm; then
    js_step "console: install" web "pnpm install --frozen-lockfile"
    js_step "console: client generated" web "pnpm generate && git diff --exit-code -- packages/api/src"
    js_step "console: typecheck" web "pnpm typecheck"
    js_step "console: lint" web "pnpm lint"
    js_step "console: format" web "pnpm format"
    js_step "console: test" web "pnpm test"
    js_step "console: catalogs" web "pnpm i18n:check"
    js_step "console: build" web "pnpm build"
  else not_run "console" "pnpm is not installed"; fi
fi
if [ -n "$SPECS" ]; then
  if need_tool pnpm && need_tool docker; then
    # shellcheck disable=SC2086
    js_step "console end to end (changed specs)" web/e2e "env -u DATABASE_URL pnpm test:local $(printf '%s ' $SPECS)"
  else not_run "console end to end" "pnpm and docker are needed"; fi
fi
if [ "$RUN_SDK_JS" = 1 ]; then
  if need_tool pnpm; then step "sdk-js" digest_tail "fix what the output says" task sdk-js:check
  else not_run "sdk-js" "pnpm is not installed"; fi
fi
if [ "$RUN_DART" = 1 ]; then
  if need_tool dart; then step "dart" digest_tail "fix what the output says" task dart:check
  else not_run "dart" "dart is not installed"; fi
fi
if [ "$RUN_FLUTTER" = 1 ]; then
  if need_tool flutter; then step "flutter" digest_tail "fix what the output says" task flutter:check
  else not_run "flutter" "flutter is not installed"; fi
fi
if [ "$RUN_PYTHON" = 1 ]; then
  if need_tool uv; then step "sdk-python" digest_tail "fix what the output says" task sdk-python:check
  else not_run "sdk-python" "uv is not installed"; fi
fi
if [ "$RUN_RUBY" = 1 ]; then
  if need_tool bundle; then step "sdk-ruby" digest_tail "fix what the output says" task sdk-ruby:check
  else not_run "sdk-ruby" "bundler is not installed"; fi
fi

lock_release
# ---------------------------------------------------------------- the table

say
say "pr-check against $(git rev-parse --short "$BASE") ($BASE_REF)"
awk -F'\t' '
  { n = $3; t = sprintf("%dm%02ds", n / 60, n % 60)
    printf "  %-9s %-52s %7s %s\n", $2, $1, t, $4 }' "$RESULTS"
if [ "$UNCOMMITTED" -gt 0 ]; then
  say "  note: $UNCOMMITTED uncommitted file(s) were checked; CI sees only what you commit and push"
fi
if [ "$FAILED" -ne 0 ]; then
  say "pr-check FAILED: fix the steps above and run it again. Do not lower a floor or skip a check."
  exit 1
fi
if [ -n "$TEST_FLAGS" ]; then
  say "pr-check passed, but the server tests ran with PR_CHECK_TEST_FLAGS=\"$TEST_FLAGS\": that is not the run CI does"
else
  say "pr-check passed"
fi
exit 0
