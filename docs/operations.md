# Operations

How to run Flagnetic in production: what it needs, how it starts and stops, how changes reach SDKs, and what to watch. [Installing Flagnetic](install.md) gets an instance running; every setting named here is listed in the [configuration reference](configuration.md).

## What it needs

- **PostgreSQL 16 or newer.** `serve` and `migrate` read the server version at start and refuse an older one with a message naming it. Flagnetic is tested on the oldest and the newest supported versions.
- **The `pg_trgm` extension.** The audit log's free-text search runs on a trigram index, and `migrate` creates the extension in the `public` schema when the database lacks it, which needs a role allowed to create extensions there (on most managed services, the default administrative role). Create it once beforehand (`CREATE EXTENSION pg_trgm WITH SCHEMA public;`, or another schema of your choice) to run migrations as a role without that right, or in a database without a `public` schema; Flagnetic uses it wherever it is. Do not create it in a schema Flagnetic migrates: an extension belongs to the whole database, and dropping the schema it is in removes the trigram index of every other Flagnetic schema in that database.
- **Synchronized clocks.** Every replica judges time by its own wall clock (expiry, leases, scheduled changes), so run NTP or your platform's time sync on every host. Coordination between replicas tolerates seconds of skew, not minutes.
- **A secret key.** `serve` refuses to start without `FLAGNETIC_SECRET_KEY`, or with one shorter than 32 bytes (exit code 2); see [The secret key](#the-secret-key).

Any number of replicas can run against one database; every replica serves every API and runs every background worker, and they coordinate only through PostgreSQL.

## Commands and exit codes

| Command | Does |
|---|---|
| `flagnetic serve` | Runs a replica. |
| `flagnetic migrate` | Applies pending migrations and exits. Safe to run again, and safe to run from several places at once. |
| `flagnetic admin bootstrap` | Creates the first instance admin and their API token; see [The first instance admin](#the-first-instance-admin). |
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

## The secret key

`FLAGNETIC_SECRET_KEY` is the instance's secret key: secrets the instance stores are sealed (encrypted) under it, and sessions are signed with it. It must be at least 32 bytes long; `serve` refuses a shorter one (exit code 2), since the keys it derives are no stronger than the key itself. Generate a long random value once, for example with `openssl rand -base64 32`, give every replica the same one, and keep it in your secret store.

**Keep the key for the life of the instance.** On its first start the instance stores a canary, a random value sealed under the key, and every replica opens it on every start. A replica given another key cannot open it and refuses to start, exit code 1, with a message naming `FLAGNETIC_SECRET_KEY`: a mistyped or rotated key stops the deploy instead of silently losing what the instance sealed. Start it again with the original key.

## The first instance admin

A fresh instance has no one who can sign in. `flagnetic admin bootstrap` creates the first instance admin, with a password and an API token, from the command line and the database's own configuration:

```sh
FLAGNETIC_ADMIN_PASSWORD='…' flagnetic admin bootstrap --email ops@example.com --name "Ops" --token-file /run/secrets/flagnetic-token --if-absent
```

- `--email` is required; `--name` defaults to the email's local part.
- The password is `FLAGNETIC_ADMIN_PASSWORD`. Without it, one is generated and printed once on standard error. A password you give must pass the password policy: 12 to 128 characters, not containing the part of the email before the `@` (in any case), and not on the list of common passwords; there are no rules about character classes. A password the policy refuses stops the command with exit code 2, naming the rule (`weak_password (length)`, `(email)` or `(common)`), before anything is created.
- The API token carries every scope and does not expire: whoever runs the command holds the database's credentials already. It is printed on standard output, or written to `--token-file` instead, so a provisioning script can pick it up without it reaching a log. The file is created new, with mode 0600, before anything else: a path that exists or cannot be created stops the command (exit code 1) with nothing created, and the token is then never printed. With `--if-absent` on an instance that has an instance admin, the file is not touched.
- Once an instance admin exists, bootstrap is refused with exit code 1, so it never adds a second one by accident. With `--if-absent` that is a success that does nothing (exit 0), which makes provisioning safe to rerun.
- Two bootstraps running at once cannot both succeed. Both changes are recorded in the audit log with the CLI as their actor.
- The schema must be migrated first (`flagnetic migrate`, or a replica started once).

Two more commands work the same way, for a second instance admin or one who lost their way in:

- `flagnetic admin create --email <email> [--name <name>]` adds an active instance admin with a password: `FLAGNETIC_ADMIN_PASSWORD` under the same policy, or one generated and printed once on standard error, as bootstrap does. An email the instance already holds is refused (exit code 1).
- `flagnetic admin promote --email <email>` makes an existing member an instance admin; for one who already is, it does nothing. An unknown email is refused (exit code 1).

Both are recorded in the audit log with the CLI as their actor.

## Signing in to the console

Members sign in with their email and password. A sign-in opens a session held by the server and referred to by the cookie `flagnetic_session` (`HttpOnly; SameSite=Lax`), signed under the secret key. A session ends at sign-out, after 24 hours without a request, or 30 days after it opened (the defaults; see below); a request with its cookie is then answered `401 session_expired` and the cookie is cleared.

An instance admin changes the instance's auth settings at runtime (`/instance/auth-settings`); they live in the database, not in the configuration:

| Setting | Default | Meaning |
|---|---|---|
| `session_idle_timeout_seconds` | 86400 (a day) | A session ends this long after its last request. |
| `session_absolute_timeout_seconds` | 2592000 (30 days) | A session ends this long after it opened, however much it is used. |
| `token_max_lifetime_seconds` | 31536000 (365 days) | The furthest ahead an API token may expire. |
| `token_expiry_required` | false | Whether an API token must be created with an expiry. |
| `password_min_length` | 12 | The fewest characters a password chosen from now on has; never below 12. |

Durations are whole seconds from a minute to ten years, and the idle timeout is at most the absolute one. Every replica applies a change within a second: each keeps a copy of the settings and drops it when PostgreSQL notifies it of a change, and reads the settings from PostgreSQL on every use while its listener is down (`flagnetic_listener_up{channel="auth_settings"}` at 0). Live sessions are judged by the timeouts in force, so lowering one signs out every session already past it; raising the absolute timeout never lengthens a session opened under a lower one. Each change is recorded in the instance audit log as `auth_settings.updated`, with the settings before and after.

In the console, an instance admin edits them under **Auth settings** in the instance navigation, each duration entered in minutes, hours, days or seconds.

- The cookie is `Secure` whenever the console is reached over HTTPS, and also over plain HTTP unless the host is `localhost` (or a loopback address), where browsers accept it without TLS. To try Flagnetic over plain HTTP on another host, set `FLAGNETIC_INSECURE_COOKIES=true`; never do so on a network you do not trust.
- Sign-in accepts only a JSON body (`415 unsupported_media_type` otherwise), which a form on another site cannot send, so no page elsewhere can sign a visitor in as a member of its choosing.
- A request signed in by the cookie, a read as much as a write, is refused (`403 cross_origin`) when the browser says it comes from another site, so a page elsewhere can neither act as a signed-in member nor read what the console shows them. Requests with an API token are not checked: a browser never sends one by itself.

Guessing passwords is slow, and cannot exhaust a replica:

- **Lockout.** Five consecutive failed sign-ins on one email lock it for a minute; each further failure after a lock ends locks it twice as long, up to an hour. A locked email is answered `429 locked` with `retry_after` (and `Retry-After`) in seconds, even with the right password. Unknown emails lock exactly like known ones, so the answer tells nobody which emails exist. A successful sign-in clears the count. Every replica honours a lockout another recorded: the counts live in PostgreSQL.
- **Throttle.** A hundred failed attempts from one source within fifteen minutes refuse it, `429 rate_limited` with `Retry-After`, until those fifteen minutes end. A source is an IPv4 address, or an IPv6 address with the rest of its /64. Behind a proxy, list it in `FLAGNETIC_TRUSTED_PROXIES`, or every member shares the proxy's address.
- Each attempt is counted as a failure before its password is checked and taken back when it was right, could not be checked, or was refused as locked or throttled, so attempts made at once cannot all slip past the count and only checked passwords count.
- **Hashing bound.** Checking a password takes 64 MiB of memory for about a tenth of a second. Each replica checks at most `FLAGNETIC_PASSWORD_HASH_CONCURRENCY` (default 4) at once; a sign-in that waits more than 2 seconds for its turn is answered `503 busy` with `Retry-After`, and is not counted as a failure. Locked, throttled and oversized attempts are refused before they wait for a turn, and the request body of an operation that checks a password is limited to 8 KiB (`413 request_too_large`). Raise the bound only with the memory to match: each slot needs 64 MiB on top of the replica's own.
- Sign-ins, sign-outs, failures (with their reason) and lockouts of members are recorded in the instance audit log; attempts on emails no member has are not.

A member sees every device they are signed in on, with its address, browser and when it was last used, and signs out one of them, every other one, or all (`/me/sessions`). An instance admin does the same for any member (`/members/{member}/sessions`), to answer an incident. Only a session can sign its own member out, through either route: an API token is refused `403 session_required`, so a leaked token, an instance admin's included, cannot lock its owner out. Every revocation is recorded in the instance audit log as `session.revoked`, naming the sessions it ended.

An instance admin manages the instance's members under `/members`: edits a member's name and email, makes them instance admin or not, and offboards them. Deactivating a member revokes every session and personal access token they hold in the same transaction; project tokens they created belong to their projects and keep working. Reactivating makes the member active again when they hold a password, and invited otherwise; nothing their deactivation revoked comes back. The instance's last active instance admin can be neither deactivated nor demoted (`409 last_instance_admin`), so the instance is never left without one. Each change is recorded in the instance audit log (`member.updated`, `member.promoted`, `member.demoted`, `member.deactivated` with how many sessions and tokens it revoked, `member.reactivated`).

An instance admin invites someone by email (`POST /members`) and is shown a one-time link to pass on, since no mail is sent: `FLAGNETIC_BASE_URL` (or, when unset, the origin the request came to) followed by `/invitations/<secret>`. Opening it, the invited person sets their first password, under the password policy, and is signed in. A link works once and for seven days; reissuing one (`POST /members/{member}/invitation`) kills the earlier link, and so does deactivating the member. Reactivating a member who never set a password invites them again, with a fresh link in the answer. The secret is stored only as its SHA-256 and never written to a log line: requests carrying it are logged with the route's pattern, `/api/v1/auth/invitations/{secret}`, as their path. Accepting counts against the address throttle like a failed sign-in until the secret proves to open an invitation, so an address hammering acceptance is throttled together with its sign-ins; a secret of 32 random bytes cannot be guessed in any case. Invitations, reissues and acceptances are recorded in the instance audit log (`member.invited`, `member.invitation_reissued`, `member.invitation_accepted`).

In the console, an instance admin does all of this under **Members** in the instance navigation: the list finds members by name or email and filters them by status, and each member's page holds the changes above and the member's sessions and personal access tokens, to revoke. A link an invitation, a reissue or a reactivation issues is shown once, in a dialog that stays until the instance admin says they stored it. The invited person opens the link in the console, sees whose invitation it is (the email masked), and sets their password there.

Every replica runs the auth retention worker once an hour. It deletes ended sessions (revoked, or past the idle or absolute timeout in force), an email's failed sign-ins a day after the last one once no lock holds it, a source's failures once its fifteen-minute window is over, and invitations used, replaced or expired more than 30 days ago, except each member's latest, a thousand rows per transaction until nothing is left. Each batch runs under an advisory lock, so one replica purges at a time; a replica that finds another purging leaves the rest of its round to it.

Every replica also runs the `token_last_use` worker once a minute. A replica gathers in memory when and from where each API token it authenticates was used, and the worker writes the latest use of each, at most one per token a minute across replicas. A replica that stops loses at most its last minute of uses; a write that fails is dropped and the token's next use is recorded instead. See [Access](access.md) for API tokens themselves.

## The console

The binary serves the web console at every path the APIs and probes do not own, so `https://flags.example.com/projects/checkout` loads the console, which then renders that page. Its files are built into the binary: there is nothing to deploy beside it. Files under `/assets/` carry a hash of their content in their name and are cached for a year; the page itself is never cached, so a new release reaches every browser on its next page load. Every answer of the console carries a content security policy that lets it load scripts, styles, fonts and images from its own origin only (images also as `data:` URIs) and call no other origin; inline styles are allowed, inline scripts are not.

The console is in English and Russian. It shows the language the member chose in their personal settings, which is kept with the member, so it follows them to every browser, and remembered in the browser for the sign-in page. A member who chose none sees the first of the browser's languages the console offers, else `FLAGNETIC_DEFAULT_LOCALE`.

## Probes

| Path | Answers | Use it for |
|---|---|---|
| `/healthz` | `200 {"status":"ok"}` while the process runs. | Liveness: restart a replica that no longer answers. |
| `/readyz` | `200 {"status":"ok"}` when PostgreSQL answers a ping within 2 seconds; `503 {"status":"unavailable"}` when it does not (logged at warn); `503 {"status":"draining"}` once the replica has been told to stop. | Readiness: route requests only to ready replicas. |

Both are served on the public listener (`FLAGNETIC_LISTEN_ADDR`). A notification listener that is down does not make a replica unready: it still serves, and the metrics and the log report the listener. In a container image without a shell, `flagnetic healthcheck` checks `/readyz` itself.

## Behind a reverse proxy

By default the client of a request is its TCP peer, and `X-Forwarded-For` and `X-Forwarded-Proto` are ignored, so a client cannot choose the address rate limits, sessions and audit entries see. Behind a load balancer or a reverse proxy, list the proxies in `FLAGNETIC_TRUSTED_PROXIES` (CIDR prefixes or single addresses, comma-separated). For a request from a listed proxy, `X-Forwarded-For` is read from the right, where each proxy appended the address it received the request from, and the first address that is not a listed proxy is the client; an entry that is not an address (some proxies write `unknown`) is skipped. The scheme is taken from the rightmost `X-Forwarded-Proto` entry when it is `http` or `https`. List only proxies you control: a listed address is believed about whoever it says the client is.

Set `FLAGNETIC_BASE_URL` to the URL members open the console at, such as `https://flags.example.com`. Its scheme decides whether the session cookie is `Secure`, whatever the proxy forwards, and its origin is trusted for the console's requests even when the proxy rewrites `Host`. Without it, each request's own scheme (from `X-Forwarded-Proto` of a trusted proxy) and host are used.

What a proxy in front must do for the ruleset stream, with an nginx configuration, is in [TLS and a reverse proxy](install.md#tls-and-a-reverse-proxy).

## HTTP limits

The public listener bounds what one client can hold:

| Setting | Default | Bound |
|---|---|---|
| `FLAGNETIC_HTTP_READ_HEADER_TIMEOUT` | `10s` | Time to send the request headers. |
| `FLAGNETIC_HTTP_BODY_READ_TIMEOUT` | `30s` | Time to send the request body; a body still arriving is answered 408. Long-lived streams are excepted. |
| `FLAGNETIC_HTTP_IDLE_TIMEOUT` | `120s` | Time a keep-alive connection may stay idle. |
| `FLAGNETIC_HTTP_MAX_BODY_BYTES` | `1048576` | Size of a request body; a larger one is answered 413. The few routes that take larger bodies declare their own limit. |

There is no bound on the time to write a response, which would cut off streams; a proxy in front may add its own. A proxy in front must not buffer the [ruleset stream](sdks/stream.md) and must allow it a read timeout above its 15-second heartbeat; see [TLS and a reverse proxy](install.md#tls-and-a-reverse-proxy).

## Delivery across replicas

Any replica serves any SDK: it can download its environment's [ruleset](sdks/ruleset.md) from one replica, hold its [ruleset stream](sdks/stream.md) on another and post [events](sdks/events.md) to a third, so a load balancer needs no sticky sessions. Replicas share nothing but PostgreSQL. Each keeps two caches in memory and learns through PostgreSQL notifications when what they hold changed:

- **Rulesets**, each environment's document by version, built from PostgreSQL once per environment however many SDKs ask at the same moment. Remote evaluation evaluates against the same document. A change is notified when its transaction commits, with the environment and its new version; every replica then builds the new document and offers its version to the streams it holds. A replica that took a write serves it at once, without waiting for its own notification.
- **SDK keys**, each key the replica has accepted, by the hash of its value. A key's revocation, rotation, a change of its allowed origins and the deletion of its environment or project are notified, and every replica drops the key; an unknown key is never cached. A rotated key's expiry is judged on every request, with no notification needed.

Deleting an environment drops its cached ruleset, ends its streams at their next heartbeat (their key is gone with it) and removes its series of `flagnetic_ruleset_propagation_seconds`.

### Notification listeners

Each replica holds one PostgreSQL connection per channel, outside its pool, for `LISTEN`. Each listener checks itself every 10 seconds by sending itself a notification, so a connection that went silent without failing (through a pooler in transaction mode, or a half-dead network path) counts as down too. `flagnetic_listener_up{channel}` tells which are up.

| Channel | Carries | While it is down |
|---|---|---|
| `rulesets` | every change to an environment's ruleset, and the deletion of an environment | The replica caches no ruleset: it builds one from PostgreSQL for every ruleset request and remote evaluation (`flagnetic_ruleset_materializations_total` grows with each). The streams it holds hear of no change made through another replica, so their SDKs learn of it only at their next poll. |
| `sdk_keys` | every change that ends or restricts an SDK key | The replica caches no key and reads every SDK request's key from PostgreSQL, so a revocation still applies at once. |
| `auth_settings` | changes to the instance's auth settings | The replica reads the settings from PostgreSQL on every use (see [Signing in to the console](#signing-in-to-the-console)). |

A listener that is down never serves stale data, only slower. When a listener connects again, the replica starts afresh: the `rulesets` listener drops every ruleset it holds, takes the versions PostgreSQL holds as the newest (even when older than versions it saw before, as after a failover that lost recent writes), and tells every stream it holds of the current version. Ruleset versions come from the clock of the replica that commits a change, so the first change after a failover or a restore is numbered above every version handed out before it, and every SDK accepts it.

### Propagation

The declared propagation time of a server SDK, from the commit of a change until the SDK holds the new ruleset, is at most 2 seconds at the 99th percentile with the ruleset stream, whichever replicas the writer and the SDK use; without the stream, at most the SDK's poll interval (see [Propagation](sdks/stream.md#propagation)).

`flagnetic_ruleset_propagation_seconds` measures the server's part, from the commit (by the clock of the replica that committed it) to each replica's first ruleset or evaluation answer carrying the version; the SDK's download comes on top. Watch its 99th percentile, for example:

```promql
histogram_quantile(0.99, sum by (le) (rate(flagnetic_ruleset_propagation_seconds_bucket[5m])))
```

Above a second or two for minutes, it points at a `rulesets` listener that is down, a database slow to build rulesets, or replica clocks out of step.

## What a client key still learns

A client key ships inside apps and browsers, where anyone can read it, so it never receives a ruleset and its [remote evaluation](sdks/evaluate.md) answers never say which rule, segment or prerequisite produced a value (see [What a key learns](sdks/compatibility.md#what-a-key-learns)). A private segment's conditions and members never leave the server. Whoever holds a client key can still learn:

- **The served values.** A client key may post any context, so posting contexts that differ in one attribute shows whether that attribute changes a flag's value, and a value that a rule on a private segment decides tells whether the posted unit is in that segment.
- **An experiment block** on a value an experiment rule assigned, even behind a private segment: it tells that the context is in the experiment.
- **The reason `override`**, which tells that the unit has an override on that flag.
- **The environment's flags**: the key, type and served variation of every flag, the environment's key and its ruleset version, which moves on with every change.
- **The declared attributes**: `ignored_attributes` tells which attribute names the project does not declare (`undeclared`) and which value types a declared one refuses (`type_mismatch`).

Nothing short of contexts a client cannot choose closes the first, so do not use a flag served to a client key to keep a secret: keep values that must not leak, and targeting that must not be probed, behind a server key.

## Rate limits on the SDK API

Every operation of the SDK API (`/sdk/v1`) is limited per SDK key, so one misbehaving app cannot exhaust a replica. A client key ships in many apps at once, so its requests are counted per key and client IP; a server key's per key, whatever the IP. Requests are counted in fixed one-minute windows, each starting with the first request counted, and streams by how many a key holds open at once:

| Setting | Default | Limit |
|---|---|---|
| `FLAGNETIC_RATE_LIMIT_CLIENT_EVALUATE` | `120` | Remote evaluations per minute of a client key and IP. |
| `FLAGNETIC_RATE_LIMIT_CLIENT_EVENTS` | `60` | Event batches per minute of a client key and IP. |
| `FLAGNETIC_RATE_LIMIT_CLIENT_STREAM_CONNECTS` | `30` | Streams opened per minute by a client key and IP. |
| `FLAGNETIC_RATE_LIMIT_CLIENT_CONCURRENT_STREAMS` | `10` | Streams a client key and IP hold open at once. |
| `FLAGNETIC_RATE_LIMIT_SERVER_RULESET` | `600` | Ruleset downloads per minute of a server key. |
| `FLAGNETIC_RATE_LIMIT_SERVER_EVALUATE` | `6000` | Remote evaluations per minute of a server key. |
| `FLAGNETIC_RATE_LIMIT_SERVER_EVENTS` | `600` | Event batches per minute of a server key. |
| `FLAGNETIC_RATE_LIMIT_SERVER_CONCURRENT_STREAMS` | `1000` | Streams a server key holds open at once; its stream connects are not limited per minute. |
| `FLAGNETIC_RATE_LIMIT_FAILED_AUTHENTICATIONS` | `30` | Refused keys per minute from one client IP. |

The failed authentications are checked before any key is looked up: once an IP presented 30 keys in a minute that were malformed, unknown, revoked or expired, every SDK API request from it is refused until the window ends, which makes guessing keys slow and cheap to refuse. A request that carries no key is not counted.

A limited answer carries `RateLimit-Limit` and `RateLimit-Remaining`; a stream's tells the budget of the streams held at once. A refusal is `429 rate_limited` with `Retry-After` and `RateLimit-Reset`, both in whole seconds of at least 1, and the problem's `retry_after`.

Each replica counts on its own, in memory: an instance of three replicas behind a load balancer lets up to three times each limit through. Size the limits for the replica count. A limit set to `off` is switched off; `0` or a negative value is refused at start, so a typo never lifts a limit silently. The client IP is the one `FLAGNETIC_TRUSTED_PROXIES` resolves, so behind a proxy list it there, or every client counts as the proxy.

### Client keys behind a shared address

The client-key limits count each key and client IP together, which assumes that one address is one device. Many devices often share one: an office network, a campus or a hotel behind one NAT address, and mobile carriers, which put thousands of phones behind each address of a carrier-grade NAT. Every app using the key behind that address then shares one budget per replica:

- **Concurrent streams.** Each app in the foreground holds a ruleset stream, so the 11th app open behind one address is refused its stream (`FLAGNETIC_RATE_LIMIT_CLIENT_CONCURRENT_STREAMS`, 10).
- **Stream connects.** When a replica drains, as in every rolling deploy, the streams it held reconnect within seconds: past 30 apps behind one address, the rest are refused until the minute ends (`FLAGNETIC_RATE_LIMIT_CLIENT_STREAM_CONNECTS`).
- **Evaluations.** Each change of the environment makes every open app evaluate once, at about the same moment: past 120 open apps behind one address, the rest are refused until the minute ends (`FLAGNETIC_RATE_LIMIT_CLIENT_EVALUATE`), and so are their evaluations on start.
- **Failed authentications.** An app still shipping a revoked or expired key is refused, and each refusal counts against its address: 30 in a minute and every SDK API request from that address is refused, the apps with a valid key included, until the minute ends (`FLAGNETIC_RATE_LIMIT_FAILED_AUTHENTICATIONS`).

A refused request is answered `429 rate_limited` with `Retry-After`, and an SDK keeps the values it holds and asks again once that time has passed, so a limit that is too low delays changes rather than breaking apps. Over IPv6, each device usually has an address of its own and is counted alone.

For mobile apps, and web apps used from large offices, raise the client-key limits to the number of app instances you expect behind one address on one replica (a load balancer spreads them over the replicas, but after a deploy one replica may hold most of them), with headroom for evaluations: for example, for up to 1,000 apps behind one address, `FLAGNETIC_RATE_LIMIT_CLIENT_CONCURRENT_STREAMS=1000`, `FLAGNETIC_RATE_LIMIT_CLIENT_STREAM_CONNECTS=1000` and `FLAGNETIC_RATE_LIMIT_CLIENT_EVALUATE=2000`. `off` switches a limit off; the failed-authentication limit still keeps guessing keys slow. Watch `flagnetic_sdk_rate_limited_total{kind="client"}` to see whether your limits refuse real apps.

## Raw events

The [events](sdks/events.md) SDKs post are stored as received in tables partitioned by the UTC day they were received. Every replica creates the partitions of today and the `FLAGNETIC_EVENTS_PARTITIONS_AHEAD` days after it (3 by default) once an hour, so events arriving after midnight never wait for one; replicas doing it at once take turns. `FLAGNETIC_EVENTS_RETENTION_DAYS` (30 by default) is the window within which an event ID is recognised as a retry and an event's time is accepted.

## Metrics

Prometheus metrics are served on `/metrics` of a listener of their own, `FLAGNETIC_METRICS_LISTEN_ADDR` (`:9090` by default), and never on the public listener. Keep the port inside your network. Setting the address empty switches metrics off and opens no port.

| Metric | Labels | What it tells |
|---|---|---|
| `flagnetic_http_requests_total` | `method`, `route`, `code` | Requests served. `route` is the matched route pattern or `unmatched`, `method` an HTTP method or `other`, so the label set stays bounded. `code` is the status answered, or `499` for a request whose client went away before it was answered, which is not a server failure and answers nothing. |
| `flagnetic_http_request_duration_seconds` | `method`, `route` | Time to serve a request (histogram). |
| `flagnetic_db_pool_acquired_conns`, `_idle_conns`, `_total_conns`, `_max_conns` | | The PostgreSQL pool of the replica. |
| `flagnetic_db_pool_acquires_total`, `_empty_acquires_total`, `_acquire_wait_seconds_total` | | Connections taken from the pool, how many had to wait, and how long they waited in total. |
| `flagnetic_worker_rounds_total`, `_failures_total`, `_panics_total` | `worker` | Rounds of each background worker, how many failed, and how many panicked. A failed or panicking round is logged and the next round runs. |
| `flagnetic_worker_round_duration_seconds` | `worker` | Time a worker's round took (histogram). |
| `flagnetic_auth_purged_rows_total` | `kind` | Rows the auth retention worker deleted: `sessions`, `email_failures`, `source_failures`, `invitations`. |
| `flagnetic_management_refusals_total` | `operation`, `code` | Requests to the management API refused before their operation ran, by the operation and the problem code: no credential (`401 unauthenticated`), a bad or ended one (`401 invalid_token`, `token_expired`, `token_revoked`, `session_expired`), another origin (`403 cross_origin`), a project token outside its project (`403 forbidden`), an API token where a session is required (`403 session_required`), a missing scope (`403 insufficient_scope`), an unknown project (`404 not_found`) and a missing permission (`403 forbidden`). See [How a request is judged](access.md#how-a-request-is-judged). |
| `flagnetic_listener_up` | `channel` | Whether the replica is listening on a PostgreSQL notification channel, through which replicas tell each other about changes: 1 listening, 0 not. Each listener checks itself every 10 seconds by sending itself a notification, so a connection that went silent without failing is caught too. |
| `flagnetic_ruleset_materializations_total` | | Rulesets the replica built from PostgreSQL. It grows by one per environment change the replica serves, and by one per request while the `rulesets` listener is down. |
| `flagnetic_ruleset_propagation_seconds` | `environment` (its identifier) | Time from a change to an environment's ruleset to this replica's first answer carrying it (histogram, 0.01 s to 60 s). A version nobody asks for is not observed. The series of a deleted environment is dropped. |
| `flagnetic_sdk_rate_limited_total` | `endpoint`, `kind` | SDK API requests refused `429 rate_limited`, by endpoint (`ruleset`, `evaluate`, `events`, `stream`) and key kind (`server`, `client`, or `none` for a client IP refused for its failed authentications before its key is known). See [Rate limits on the SDK API](#rate-limits-on-the-sdk-api). |
| `flagnetic_events_ingested_total` | `kind` | [Events](sdks/events.md) SDKs posted that were stored, by kind (`evaluation_summary`, `alias`). |
| `flagnetic_events_dropped_total` | `reason` | Events SDKs posted that were not stored, by reason: a rejection code, `duplicate` for an event received before, or `unknown_flag` for a summary row naming a flag the project does not have. |
| `flagnetic_sdk_inventory_dropped_total` | `reason` | Sightings of SDKs the [SDK inventory](sdks/keys.md#which-sdks-use-an-environment) did not record, by reason: `key_full` for a new row of a key that holds 200 rows already, `queue_full` for a new row while the replica held 1,024 rows waiting for its round, `write_failed` for sightings lost because writing them failed. Each round that drops any also logs one warning, `SDK inventory sightings dropped`, with the counts. |
| `go_*`, `process_*` | | The Go runtime and the process. |

Worth an alert: `flagnetic_listener_up` at 0 for more than a minute, which leaves that replica unable to trust what it caches and serving from PostgreSQL directly, often because something between the replica and PostgreSQL (a connection pooler in transaction mode, say) does not carry `LISTEN`; any increase of `flagnetic_worker_panics_total`, which is a bug to report with the stack from the log; `flagnetic_worker_failures_total` growing round after round for one worker, which is work not getting done; and `flagnetic_db_pool_acquired_conns` close to `flagnetic_db_pool_max_conns` for minutes, or a growing rate of `flagnetic_db_pool_acquire_wait_seconds_total`: the pool is saturated and requests will start to time out. Raise `FLAGNETIC_DB_MAX_CONNS` within what PostgreSQL's `max_connections` allows across all replicas, or add replicas.

## Logs

Logs go to standard error, one JSON object per line by default (`FLAGNETIC_LOG_FORMAT=text` for a terminal), at `FLAGNETIC_LOG_LEVEL`. Secrets are written as `[redacted]`, and `DATABASE_URL` is logged without its password.

Every response carries an `X-Request-ID` header: the one the request came with when it is well formed (letters, digits and `._:-`, at most 128 characters), otherwise a new one. Every log line written while serving a request carries it as `request_id`, together with the matched `route`, so one request's lines can be found together.

Each request is logged once when it completes, as a `request` record with `method`, `route` (the matched route pattern, or `unmatched`), `path`, `status`, `duration`, `bytes` and `client_ip` (see [behind a reverse proxy](#behind-a-reverse-proxy)). `/healthz` and `/readyz` are left out. An unexpected error is answered with a bare `internal` problem and logged at error with its cause under the same request ID, so a client can report the ID and you can find the cause. A request whose client went away before it was answered (a browser navigating away, say) is logged with `status` 499 and its cause only at debug, as `request canceled by the client`: it is not a failure, so it never shows as a 500. A request that ran past a deadline of the server's, or that the replica ended when it stopped (see [stopping](#stopping)), is still a 500 logged at error.

## Stopping

On SIGTERM or SIGINT a replica stops in three steps:

1. **Drain** (`FLAGNETIC_SHUTDOWN_DRAIN`, 5 s by default): `/readyz` answers 503 at once, while every request that still arrives is served. Every [ruleset stream](sdks/stream.md) the replica holds is closed, and its SDK reconnects, usually to another replica. This gives load balancers time to notice and route away, so a rolling deploy never refuses a request. `0` skips the drain.
2. **Grace** (`FLAGNETIC_SHUTDOWN_GRACE`, 15 s by default): the replica stops accepting connections and lets requests in flight finish.
3. **Cancel**: requests still running when the grace period ends are cancelled, background work is stopped, and the database pool closes.

A second signal skips whatever is left of the drain and the grace and goes straight to the cancel. Either way, a request the replica cancels is logged at error and counted as a 500: its client was still waiting for it.

Give the platform a stop timeout of at least drain plus grace plus a margin before it kills the process: 25 seconds for the defaults (`stop_grace_period` in Compose, `terminationGracePeriodSeconds` in Kubernetes).

A second SIGTERM or SIGINT at any point exits at once with code 1, for an operator at a terminal who does not want to wait.

## Docker Compose

[`deploy/compose.yaml`](../deploy/compose.yaml) runs PostgreSQL, a one-shot `migrate`, and the server with migrations on start turned off, a health check through `flagnetic healthcheck` and a stop grace covering the defaults. [Installing Flagnetic](install.md#with-docker-compose) walks through it.
