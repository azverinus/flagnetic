# SDK end to end

Every SDK proves itself once against the real `flagnetic` binary. This directory brings that binary up the same way for every language: two replicas on one PostgreSQL, an instance admin, and a project seeded through the management API. The Go SDK and its OpenFeature provider run as this module's own test (`task e2e`); an SDK in any other language drives the same harness through the `flagnetic-e2e` command and asserts the same things in its own test framework.

## Bringing it up

```bash
go build -o "$RUNNER_TEMP/flagnetic-e2e" ./e2e/cmd/flagnetic-e2e
FLAGNETIC_E2E_SEED="$RUNNER_TEMP/seed.json" "$RUNNER_TEMP/flagnetic-e2e" up &
```

Run the built command rather than `go run`: `go run` does not pass SIGTERM on to the program it started, so stopping it would leave the replicas running.

`up` builds `flagnetic` from the checkout (or takes `FLAGNETIC_E2E_BINARY`), applies the migrations once, starts replicas A and B on loopback, bootstraps the instance admin, seeds the project and writes the seed to `FLAGNETIC_E2E_SEED` (stdout when unset) within 120 seconds, or exits 1. It then serves until interrupted (SIGINT or SIGTERM stops both replicas and the database it started). The database is `DATABASE_URL`, or a throwaway PostgreSQL in Docker when unset; `up` migrates, bootstraps and seeds whatever database `DATABASE_URL` names, so point it only at a test database of its own. The replicas' logs go to `FLAGNETIC_E2E_LOGS`, next to the state by default. `task e2e:up` does the same with a throwaway database.

Running `up` again on the same database is idempotent: the project, segment and flags are found by key and written only where they differ from the seed; the SDK keys are new, and the earlier ones named `sdk-e2e` are revoked.

## The seed

One JSON document. Read it by field name and ignore fields you do not know: fields are only ever added.

```json
{
  "replicas": {"a": "http://127.0.0.1:41873", "b": "http://127.0.0.1:41875"},
  "project": "sdk-e2e",
  "environment": "development",
  "server_key": "fn_srv_…",
  "client_key": "fn_cli_…",
  "unit_type": "user",
  "segment": "e2e-staff",
  "flags": {"on": "e2e-on", "rollout": "e2e-rollout", "private_segment": "e2e-staff-only", "off": "e2e-off"},
  "units": {"segment_member": "staff-1", "in_rollout": "user-4", "outside_rollout": "user-1"},
  "cases": [
    {"name": "a unit inside the rollout gets its variation", "flag": "e2e-rollout", "type": "string",
     "context": {"units": {"user": "user-4"}},
     "value": "treatment", "variation": "treatment", "reason": "rollout", "client_reason": "targeting_match"}
  ]
}
```

- `replicas`: the base URLs of replica A and replica B; the SDK API is under `/sdk/v1`.
- `server_key`, `client_key`: the environment's SDK keys, one of each kind.
- `segment`: a private segment; `flags` and `units` name things by role, so a test never hard-codes a key. The rollout units are computed from the rollout's salt by the published bucketing formula.
- `cases`: every evaluation a smoke asserts, with the flag's `type`. `value` and `variation` are the same for both keys; `reason` is what a server key's local evaluation gives, `client_reason` what a client key is answered (`targeting_match` wherever targeting decided the value, since a client key never learns which rule, rollout or segment did).

## The smoke an SDK runs

1. **Read the seed** from `FLAGNETIC_E2E_SEED`.
2. **Ready.** Construct the client with replica A or B and the key of its mode (`server_key` for a server SDK, `client_key` for a client SDK; a language with both modes runs the list once per mode). Waiting for ready reaches the fresh stage, well within 10 seconds.
3. **Every case.** Evaluate each case's `flag` for its `context` (a client SDK sets the context first) with a default of the case's `type` that the flag never serves. The value, the variation and the reason of the mode (`reason` or `client_reason`) must be the case's; none may come back as a fallback.
4. **Kill switch.** With the SDK streaming from replica B, run `flagnetic-e2e kill <flags.on>`. It prints `committed_at`; measure from it until the SDK serves `false` with reason `off` (a client SDK after its change notification or a refresh), assert at most 2000 ms for a server SDK, and print the measurement as one JSON line naming the SDK as the inventory does, `{"sdk": "flagnetic-ruby", "kill_switch_ms": 312}`, to stdout and, in CI, to the job summary. Run `flagnetic-e2e restore` afterwards.
5. **Inventory.** Once the SDK has made requests, `flagnetic-e2e inventory` must list an entry whose `sdk_name` and `sdk_version` are the SDK's own within 90 seconds: each replica writes the SDKs it saw once a minute.
6. **Revocation**, last, since it ends the key: `flagnetic-e2e revoke server` (or `client`) and assert that the SDK is refused on replica B within a few seconds: a new client against B stays at built-in defaults.

## Control commands

Each reads the state `up` wrote (`FLAGNETIC_E2E_STATE`, a file in the system temporary directory by default) and acts through replica A's management API, never on the database. Each is idempotent; output is one JSON line on stdout, errors go to stderr. Exit status 0 means success, 1 a failure, 2 a usage error.

| Command | Does |
|---|---|
| `flagnetic-e2e kill <flag>` | switches the flag off: `{"flag", "changed", "committed_at"}`; `changed` is false when it was off already |
| `flagnetic-e2e restore` | writes the seed's segment and flag configurations back, keeping the keys; prints nothing |
| `flagnetic-e2e revoke <server\|client>` | revokes the seed's key of that kind; prints nothing |
| `flagnetic-e2e inventory` | the environment's SDK inventory, as the management API lists it |
| `flagnetic-e2e seed` | seeds the running instance again, keeping its keys, and prints the seed |

## In CI

The `sdk-e2e` job of `.github/workflows/ci.yml` runs on every pull request against a PostgreSQL service and runs this module's test, which brings the instance up in-process and also exercises the command. A language adds steps that start `flagnetic-e2e up` in the background with `FLAGNETIC_E2E_SEED` set, wait for the seed file, and run its smoke, as the Ruby SDK's steps do.
