# Operations

How to run Flagnetic in production: what it needs, how it starts and stops, and what to watch. Every setting named here is listed in the [configuration reference](configuration.md).

## What it needs

- **PostgreSQL 16 or newer.** `serve` and `migrate` read the server version at start and refuse an older one with a message naming it. Flagnetic is tested on the oldest and the newest supported versions.
- **Synchronized clocks.** Every replica judges time by its own wall clock (expiry, leases, scheduled changes), so run NTP or your platform's time sync on every host. Coordination between replicas tolerates seconds of skew, not minutes.

Any number of replicas can run against one database; every replica serves every API and runs every background worker, and they coordinate only through PostgreSQL.

## Commands and exit codes

| Command | Does |
|---|---|
| `flagnetic serve` | Runs a replica. |
| `flagnetic migrate` | Applies pending migrations and exits. Safe to run again, and safe to run from several places at once. |
| `flagnetic healthcheck` | Asks the replica on the configured listen address whether it is ready; exits 0 when it is, 1 otherwise. |
| `flagnetic version` | Prints the version. |

Exit codes: `0` success, `1` a runtime failure (PostgreSQL unreachable or too old, a migration failed, a port taken), `2` a usage or configuration error (an unknown command, a malformed or missing setting). A deploy that exits 2 will not get better by restarting. `healthcheck` is the exception: it exits 0 or 1 only, a refused configuration included, because container runtimes read any other code as something else than unhealthy.

## Migrations

By default `serve` applies pending migrations on start, so an upgrade is a redeploy. Replicas that start together take turns through a PostgreSQL lock, and each migration is applied once.

To run migrations as a separate step instead, for example as a job before a rolling deploy, run `flagnetic migrate` with the new version and start the replicas with `FLAGNETIC_AUTO_MIGRATE=false`.

With `FLAGNETIC_AUTO_MIGRATE=false`, `serve` compares the database's schema with the migrations the binary carries before it opens any port:

- **Behind** (the job has not run yet): `serve` refuses to start, exit code 1, naming both versions and `flagnetic migrate`. New code never meets an old schema.
- **Ahead** (a binary rolled back after a newer one migrated): `serve` starts and logs a warning naming both versions. Migrations only add to the schema in a way older binaries ignore, so a rollback keeps working.
- **Equal**: `serve` starts without a word.

## Probes

| Path | Answers | Use it for |
|---|---|---|
| `/healthz` | `200 {"status":"ok"}` while the process runs. | Liveness: restart a replica that no longer answers. |
| `/readyz` | `200 {"status":"ok"}` when PostgreSQL answers a ping within 2 seconds; `503 {"status":"unavailable"}` when it does not (logged at warn); `503 {"status":"draining"}` once the replica has been told to stop. | Readiness: route requests only to ready replicas. |

Both are served on the public listener (`FLAGNETIC_LISTEN_ADDR`). In a container image without a shell, `flagnetic healthcheck` checks `/readyz` itself.

## Behind a reverse proxy

By default the client of a request is its TCP peer, and `X-Forwarded-For` and `X-Forwarded-Proto` are ignored, so a client cannot choose the address rate limits, sessions and audit entries see. Behind a load balancer or a reverse proxy, list the proxies in `FLAGNETIC_TRUSTED_PROXIES` (CIDR prefixes or single addresses, comma-separated). For a request from a listed proxy, `X-Forwarded-For` is read from the right, where each proxy appended the address it received the request from, and the first address that is not a listed proxy is the client; an entry that is not an address (some proxies write `unknown`) is skipped. The scheme is taken from the rightmost `X-Forwarded-Proto` entry when it is `http` or `https`. List only proxies you control: a listed address is believed about whoever it says the client is.

## HTTP limits

The public listener bounds what one client can hold:

| Setting | Default | Bound |
|---|---|---|
| `FLAGNETIC_HTTP_READ_HEADER_TIMEOUT` | `10s` | Time to send the request headers. |
| `FLAGNETIC_HTTP_BODY_READ_TIMEOUT` | `30s` | Time to send the request body; a body still arriving is answered 408. Long-lived streams are excepted. |
| `FLAGNETIC_HTTP_IDLE_TIMEOUT` | `120s` | Time a keep-alive connection may stay idle. |
| `FLAGNETIC_HTTP_MAX_BODY_BYTES` | `1048576` | Size of a request body; a larger one is answered 413. The few routes that take larger bodies declare their own limit. |

There is no bound on the time to write a response, which would cut off streams; a proxy in front may add its own.

## Logs

Logs go to standard error, one JSON object per line by default (`FLAGNETIC_LOG_FORMAT=text` for a terminal), at `FLAGNETIC_LOG_LEVEL`. Secrets are written as `[redacted]`, and `DATABASE_URL` is logged without its password.

Every response carries an `X-Request-ID` header: the one the request came with when it is well formed (letters, digits and `._:-`, at most 128 characters), otherwise a new one. Every log line written while serving a request carries it as `request_id`, together with the matched `route`, so one request's lines can be found together.

Each request is logged once when it completes, as a `request` record with `method`, `route` (the matched route pattern, or `unmatched`), `path`, `status`, `duration`, `bytes` and `client_ip` (see [behind a reverse proxy](#behind-a-reverse-proxy)). `/healthz` and `/readyz` are left out. An unexpected error is answered with a bare `internal` problem and logged at error with its cause under the same request ID, so a client can report the ID and you can find the cause.

## Stopping

On SIGTERM or SIGINT a replica stops in three steps:

1. **Drain** (`FLAGNETIC_SHUTDOWN_DRAIN`, 5 s by default): `/readyz` answers 503 at once, while every request that still arrives is served. This gives load balancers time to notice and route away, so a rolling deploy never refuses a request. `0` skips the drain.
2. **Grace** (`FLAGNETIC_SHUTDOWN_GRACE`, 15 s by default): the replica stops accepting connections and lets requests in flight finish.
3. **Cancel**: requests still running when the grace period ends are cancelled, background work is stopped, and the database pool closes.

Give the platform a stop timeout of at least drain plus grace plus a margin before it kills the process: 25 seconds for the defaults (`stop_grace_period` in Compose, `terminationGracePeriodSeconds` in Kubernetes).

A second SIGTERM or SIGINT at any point exits at once with code 1, for an operator at a terminal who does not want to wait.

## Docker Compose

[`deploy/compose.yaml`](../deploy/compose.yaml) runs PostgreSQL, a one-shot `migrate`, and the server with migrations on start turned off, a health check through `flagnetic healthcheck` and a stop grace covering the defaults:

```bash
cd deploy
docker compose up
```

The server answers on port 8080. Set `FLAGNETIC_VERSION` to pin an image version; `docker compose build` builds the image from the checkout instead.
