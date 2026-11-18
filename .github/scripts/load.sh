#!/usr/bin/env bash
# Brings up two replicas on one PostgreSQL with `flagnetic-e2e up`, runs the
# SDK API load scenarios against them and stops the replicas. Extra arguments
# go to flagnetic-load (--scenario, --profile, --report-only).
#
# The database is DATABASE_URL, or a throwaway PostgreSQL in Docker when
# unset; `up` migrates and seeds whatever database it names, so point it
# only at a test database of its own. The replicas run with the SDK API's
# rate limits off: the limits protect a replica from one key, and this
# measures what a replica does when many keys are not the problem.
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
up_pid=""
cleanup() {
  if [ -n "$up_pid" ] && kill -0 "$up_pid" 2>/dev/null; then
    kill -TERM "$up_pid" 2>/dev/null || true
    wait "$up_pid" 2>/dev/null || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

export FLAGNETIC_E2E_STATE="$work/state.json"
export FLAGNETIC_E2E_SEED="$work/seed.json"
export FLAGNETIC_E2E_LOGS="${FLAGNETIC_E2E_LOGS:-$work/replicas.log}"
for limit in CLIENT_EVALUATE CLIENT_EVENTS CLIENT_STREAM_CONNECTS CLIENT_CONCURRENT_STREAMS CLIENT_STREAMS_PER_REPLICA \
  SERVER_RULESET SERVER_EVALUATE SERVER_EVENTS SERVER_CONCURRENT_STREAMS FAILED_AUTHENTICATIONS; do
  export "FLAGNETIC_RATE_LIMIT_$limit=off"
done

# Run the built commands rather than `go run`: `go run` does not pass SIGTERM
# on to the program it started, so stopping it would leave the replicas up.
(cd "$root/e2e" && go build -o "$work/flagnetic-e2e" ./cmd/flagnetic-e2e && go build -o "$work/flagnetic-load" ./cmd/flagnetic-load)
# exec, so that up_pid is the process to stop, not a subshell around it.
(cd "$root" && exec "$work/flagnetic-e2e" up >/dev/null) &
up_pid=$!

for _ in $(seq 1 240); do
  [ -s "$FLAGNETIC_E2E_SEED" ] && break
  if ! kill -0 "$up_pid" 2>/dev/null; then
    echo "load: flagnetic-e2e up exited before the seed was written; the replicas' log:" >&2
    tail -40 "$FLAGNETIC_E2E_LOGS" >&2 || true
    exit 1
  fi
  sleep 0.5
done
[ -s "$FLAGNETIC_E2E_SEED" ] || { echo "load: no seed within 120 seconds" >&2; exit 1; }

status=0
"$work/flagnetic-load" "$@" || status=$?
if [ "$status" -ne 0 ] && [ -n "${GITHUB_ACTIONS:-}" ]; then
  echo "::group::the replicas' log"
  tail -100 "$FLAGNETIC_E2E_LOGS" || true
  echo "::endgroup::"
fi
exit "$status"
