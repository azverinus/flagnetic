# Installing Flagnetic

Flagnetic is one binary, `flagnetic`, with the web console built in, and PostgreSQL is the only thing it needs. This guide installs it with Docker Compose or as a binary, puts it behind TLS and a reverse proxy, sizes its PostgreSQL connections and upgrades it. [Operations](operations.md) covers running it once it is up, and the [configuration reference](configuration.md) lists every setting.

Flagnetic 0.2 is a developer preview: try it, build against it, and do not run it where an outage would hurt. See [what 0.x promises](sdks/compatibility.md#what-0x-promises) before you rely on it.

## What you need

- **PostgreSQL 16 or later**, with the `pg_trgm` extension available (Flagnetic creates it when its role may; see [What it needs](operations.md#what-it-needs)). A managed service works.
- **A secret key**, generated once and kept for the life of the instance: `openssl rand -base64 32`. Every replica needs the same one; see [The secret key](operations.md#the-secret-key).
- **Synchronized clocks** on every host running a replica.
- For production, **TLS** in front: Flagnetic itself serves plain HTTP.

## With Docker Compose

[`deploy/compose.yaml`](../deploy/compose.yaml) runs PostgreSQL, a one-shot `migrate` job and one replica, with a health check and a stop timeout that covers the drain. It is the quickest way to try Flagnetic on one machine, and a starting point for your own deployment.

```bash
git clone https://github.com/azverinus/flagnetic.git
cd flagnetic/deploy
echo "FLAGNETIC_SECRET_KEY=$(openssl rand -base64 32)" > .env   # once; keep this file
docker compose up -d
```

Compose reads the secret key from `.env` and refuses to start without it. The server image is `ghcr.io/azverinus/flagnetic`, for `linux/amd64` and `linux/arm64`, with build provenance and an SBOM. The compose file pulls `latest`; to pin a release, add it to `.env` before `docker compose up -d`:

```bash
echo "FLAGNETIC_VERSION=0.2.0" >> .env
```

`docker compose build` builds the image from your checkout instead.

Then create the first instance admin; the command prints a generated password once, and an API token:

```bash
docker compose exec flagnetic flagnetic admin bootstrap --email you@example.com
```

Open http://localhost:8080 and sign in with that email and password. The console is on port 8080; metrics are on port 9090 of this host only.

Over plain HTTP the console works on `localhost` only, because the session cookie is `Secure` anywhere else. To reach a trial from another machine, put TLS in front (below), or set `FLAGNETIC_INSECURE_COOKIES=true` under the `flagnetic` service's `environment` on a network you trust.

The PostgreSQL of this file keeps its data in the `postgres` volume, with a fixed password and no backups. For anything you want to keep, point `DATABASE_URL` at a PostgreSQL you back up, and remove the `postgres` service. To take a copy of this one:

```bash
docker compose exec -T postgres pg_dump -U flagnetic flagnetic > flagnetic.sql
```

## As a binary

### Get the binary

From the release image, for Linux (add `--platform linux/arm64` to `docker create` for ARM):

```bash
docker create --name flagnetic-binary ghcr.io/azverinus/flagnetic:0.2.0
docker cp flagnetic-binary:/usr/local/bin/flagnetic ./flagnetic
docker rm flagnetic-binary
```

Or built from source, for any platform Go supports, with Go, Node.js, pnpm and [Task](https://taskfile.dev):

```bash
git clone --branch v0.2.0 https://github.com/azverinus/flagnetic.git
cd flagnetic
task build        # the console, then bin/flagnetic embedding it
```

The console is inside the binary: there is nothing else to deploy beside it. `flagnetic version` prints its version.

### Configure it

Every setting is an environment variable, and all but a few may also be given in a YAML file named by `FLAGNETIC_CONFIG_FILE`; a variable wins over the file. Only `DATABASE_URL` and, for `serve`, `FLAGNETIC_SECRET_KEY` are required. A replica behind a proxy at `https://flags.example.com` might use:

```yaml
# /etc/flagnetic/flagnetic.yaml
database_url: postgres://flagnetic@db.internal:5432/flagnetic?sslmode=require
base_url: https://flags.example.com
trusted_proxies: 10.0.0.2
metrics_listen_addr: "127.0.0.1:9090"
auto_migrate: false
```

with the database password and the secret key from your secret store, as variables:

```bash
export FLAGNETIC_CONFIG_FILE=/etc/flagnetic/flagnetic.yaml
export PGPASSWORD='…'                 # read by the PostgreSQL driver
export FLAGNETIC_SECRET_KEY='…'
```

A setting that does not parse, or a file key Flagnetic does not know, stops the start with exit code 2 and a message naming it.

### Run it

```bash
flagnetic migrate        # apply the migrations; safe to run again
flagnetic serve          # run a replica
```

With `auto_migrate` left at its default (`true`), `serve` applies pending migrations itself and `migrate` is not needed. Then create the first instance admin once, from any host that reaches the database:

```bash
flagnetic admin bootstrap --email you@example.com
```

`flagnetic healthcheck` exits 0 when the replica on the configured listen address is ready, for a supervisor that cannot make HTTP requests.

Under systemd, a unit could be:

```ini
# /etc/systemd/system/flagnetic.service
[Unit]
Description=Flagnetic
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=/usr/local/bin/flagnetic serve
EnvironmentFile=/etc/flagnetic/flagnetic.env
User=flagnetic
Restart=on-failure
# Exit code 2 is a configuration error: restarting will not fix it.
RestartPreventExitStatus=2
# The drain and the grace period with a margin (5 s + 15 s by default).
TimeoutStopSec=25s

[Install]
WantedBy=multi-user.target
```

`/etc/flagnetic/flagnetic.env` holds the variables, one `NAME=value` per line, readable by the `flagnetic` user only.

## More than one replica

Run as many replicas as you like against one database, each with the same configuration and secret key, behind a load balancer. Every replica serves every API, the console and the SDK API, and runs every background worker; replicas tell each other about changes through PostgreSQL notifications, so **no sticky sessions are needed**, an SDK may reach any replica on each request, and a change made through one replica reaches SDKs on every other within the [declared propagation time](sdks/stream.md#propagation). Route only to ready replicas (`/readyz`), and give each a stop timeout of at least 25 seconds so it can [drain](operations.md#stopping).

Rate limits of the SDK API are counted by each replica on its own: size them for the replica count ([Rate limits on the SDK API](operations.md#rate-limits-on-the-sdk-api)).

## TLS and a reverse proxy

Flagnetic serves plain HTTP; terminate TLS in a reverse proxy or load balancer in front of it. Whatever proxy you use:

- **Tell Flagnetic about it.** List the proxy's addresses in `FLAGNETIC_TRUSTED_PROXIES` so its `X-Forwarded-For` and `X-Forwarded-Proto` are believed: otherwise every client is seen with the proxy's address, and rate limits, the sign-in throttle and the audit log all see one client. Set `FLAGNETIC_BASE_URL` to the public URL (`https://flags.example.com`), so the session cookie is `Secure` and invitation links point at the right host. See [Behind a reverse proxy](operations.md#behind-a-reverse-proxy).
- **Do not buffer the ruleset stream.** `GET /sdk/v1/stream` is a long-lived `text/event-stream` answer; a proxy that buffers it holds every version back. Turn response buffering off for that path (Flagnetic sends `X-Accel-Buffering: no`, which nginx honours by itself).
- **Read timeout above 15 seconds.** The ruleset stream sends a heartbeat every 15 seconds and nothing else while nothing changes, so a proxy must not close a connection idle for 15 seconds; 60 seconds leaves a margin. Each ruleset stream lasts up to 65 minutes before the server closes it; a proxy that caps connections sooner only makes SDKs reconnect sooner, which they do without losing a version.
- **Keep the metrics port off it.** Proxy the public listener (`FLAGNETIC_LISTEN_ADDR`, `:8080`) only; scrape `/metrics` on `FLAGNETIC_METRICS_LISTEN_ADDR` from inside your network.
- **Compression is optional.** Flagnetic compresses SDK API answers itself when asked.

An nginx configuration for two replicas:

```nginx
upstream flagnetic {
    server 10.0.0.11:8080;
    server 10.0.0.12:8080;
    keepalive 16;
}

server {
    listen 80;
    server_name flags.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name flags.example.com;
    ssl_certificate     /etc/nginx/tls/flags.example.com.crt;
    ssl_certificate_key /etc/nginx/tls/flags.example.com.key;

    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    location / {
        proxy_pass http://flagnetic;
    }

    # The ruleset stream: pass each frame on as it comes, and wait past the
    # 15-second heartbeat before giving up on the connection.
    location /sdk/v1/stream {
        proxy_pass http://flagnetic;
        proxy_buffering off;
        proxy_read_timeout 60s;
    }
}
```

with `FLAGNETIC_TRUSTED_PROXIES` set to the nginx host's address and `FLAGNETIC_BASE_URL=https://flags.example.com` on each replica.

## PostgreSQL connections

### Poolers in session mode only

Each replica listens on PostgreSQL notification channels (`LISTEN`) to learn of changes made through other replicas: rulesets, SDK keys and auth settings. A connection pooler in transaction or statement mode, such as PgBouncer with `pool_mode = transaction`, hands the listening session to other clients and delivers nothing. Connect Flagnetic to PostgreSQL directly, or through a pooler in **session mode**.

Through a pooler that does not carry `LISTEN`, Flagnetic still works but slowly and late: each listener checks itself every 10 seconds, finds that its own notifications do not come back, and reports itself down (`flagnetic_listener_up` at 0); the replica then trusts no cache and reads PostgreSQL on every SDK request, and the ruleset streams it holds hear of no change made through another replica, so their SDKs learn of it only at their next poll (every 5 minutes for a Go SDK holding the ruleset stream, by default) and the [declared propagation time](sdks/stream.md#propagation) does not hold. Session mode is required for the 2 seconds.

### The connection budget

Each replica opens at most:

- `FLAGNETIC_DB_MAX_CONNS` connections in its pool (10 by default), and
- 3 listener connections of its own, outside the pool, one per notification channel, and
- 2 connections outside the pool on which [protection](governance.md#what-protection-costs) reads what an environment served before a write, opened only when needed.

So an instance takes `replicas × (FLAGNETIC_DB_MAX_CONNS + 5)` connections, plus a pool's worth while a `flagnetic migrate` job runs and whatever your own tools use. Keep the total below PostgreSQL's `max_connections` minus its reserved connections (`superuser_reserved_connections`, 3 by default). Three replicas at the default take 45; with PostgreSQL's default `max_connections` of 100 that leaves room for a migration job, backups and an operator's `psql`.

A pool that is too small for the load shows as `flagnetic_db_pool_acquired_conns` near `flagnetic_db_pool_max_conns` (see [Metrics](operations.md#metrics)).

## Health and readiness

| Path | Use it for |
|---|---|
| `/healthz` | Liveness: answers `200` while the process runs. |
| `/readyz` | Readiness: `200` while PostgreSQL answers within 2 seconds, `503` when it does not and while the replica drains. |

Both are on the public listener. A load balancer routes only to ready replicas; a supervisor restarts one whose `/healthz` stops answering. A notification listener that is down does not make a replica unready: it keeps serving, reading PostgreSQL directly. See [Probes](operations.md#probes).

## Upgrades

Read the release notes of every version between yours and the new one: a change that needs you to act is marked there.

1. **Back up the database.** A backup taken before the upgrade is the way back if anything goes wrong.
2. **Stop every replica.** In this developer preview, upgrade all replicas together and accept a brief downtime: 0.x does not promise that two versions serve side by side.
3. **Migrate.** Run `flagnetic migrate` with the new version, as a job. Or leave `FLAGNETIC_AUTO_MIGRATE` on: the first new replica migrates, and replicas starting together take turns. With Compose, set the new `FLAGNETIC_VERSION` in `.env` and run `docker compose pull && docker compose up -d`: the `migrate` job runs again before the new server starts.
4. **Start the new replicas.**

From 0.1.0 on, migrations only move forward, without data loss: a migration is never edited once released, and there are no down migrations. 0.x does not promise that an older version keeps working against a database a newer one migrated: to go back, restore the backup taken before the upgrade and deploy the previous version. A replica never runs against a schema older than its own: with auto-migration off it refuses to start until `flagnetic migrate` has run (see [Migrations](operations.md#migrations)).

The SDK API (`/sdk/v1`) and the ruleset format only grow, so SDKs keep working across server upgrades without being upgraded themselves. The management API (`/api/v1`) and the console may still change incompatibly between 0.x releases, each such change marked as breaking in the release notes; scripts using the management API should be checked against them.
