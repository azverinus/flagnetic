# SDK keys

An SDK key identifies one environment to an SDK. Each environment has its own keys, of one of two kinds:

- A **server key** (`fn_srv_…`) downloads the environment's ruleset, private segments included. It belongs on a server and never in a browser or an app.
- A **client key** (`fn_cli_…`) never receives a ruleset, only the values remote evaluation serves it, so it may be shipped in a browser or a mobile app. It may be limited to allowed origins.

A key's value is the kind's prefix and 40 random characters. It is shown once, when the key is created or is a rotation's replacement, and never again: Flagnetic keeps only its SHA-256. Every key is listed by its **key ID**, the first 8 characters after the prefix, so you can tell which key a service holds without seeing the value.

## Presenting a key

An SDK sends its key as a bearer token on every request to the SDK API, `/sdk/v1`: `Authorization: Bearer fn_srv_…`. A key that does not work is refused `401`, with a problem whose `code` says why and the challenge of RFC 6750 in `WWW-Authenticate`:

| Code | When | `WWW-Authenticate` |
|---|---|---|
| `sdk_key_missing` | no key, or an `Authorization` of another scheme | `Bearer` |
| `sdk_key_invalid` | a malformed or unknown value, refused alike | `Bearer error="invalid_token", error_description="sdk_key_invalid"` |
| `sdk_key_revoked` | a revoked key | `Bearer error="invalid_token", error_description="sdk_key_revoked"` |
| `sdk_key_expired` | a rotated key past its overlap | `Bearer error="invalid_token", error_description="sdk_key_expired"` |

A key the server cannot check because its database does not answer is `503 unavailable` with `Retry-After`, never a `401`, so an SDK keeps what it holds and retries. A key of a kind an operation does not serve, such as a client key asking for the ruleset, is `403 key_kind_not_allowed`.

Each replica keeps the keys it has accepted in memory, and every replica drops a key within a second of its revocation, its rotation, a change of its allowed origins or the deletion of its environment or project, whichever replica the change went through. A replica that is not sure it hears of every change (its notification listener is down) keeps nothing and checks every key against the database. A rotated key's expiry is judged on every request, so it ends on time with no change to hear of. A key's `last_used_at` is written at most once a minute.

## Managing keys

The keys of an environment are under `/api/v1/projects/{project}/environments/{environment}/sdk-keys`. Listing them needs `project.read`; every change needs `sdk_key.manage`, which maintainers and owners hold and which a project token or a personal access token carrying the `project:admin` scope exercises too, so a pipeline provisioning an environment can give its services keys (see [Access](../access.md)).

| Operation | Request | Answer |
|---|---|---|
| List | `GET …/sdk-keys` | every key, the oldest first, with its status; never a value |
| Create | `POST …/sdk-keys` with `kind`, `name` and, for a client key, `allowed_origins` | `201` with the key and its `value` |
| Rename, change origins | `PATCH …/sdk-keys/{sdk_key}` with `allowed_origins` (required) and optionally `name` | `200` with the key |
| Rotate | `POST …/sdk-keys/{sdk_key}/rotate`, optionally with `overlap_seconds` | `201` with the replacement and its `value` |
| Revoke | `DELETE …/sdk-keys/{sdk_key}` | `200` with the key, revoked |

An environment holds at most 10 active keys of each kind; one more is refused `409 sdk_key_limit_reached`. A rotated or revoked key does not count, so revoke or rotate one first. A body that breaks a rule is refused `422 invalid_sdk_key`, with a JSON Pointer to each field to fix.

## In the console

Open an environment from the Environments list; its **SDK keys** tab shows every key with its name, kind, key ID, status, creation, last use, expiry and allowed origins, never its value. A maintainer or an owner creates a key (its kind, its name and, for a client key, its allowed origins), rotates an active key with an overlap of none, 1 hour, 24 hours or 7 days (7 days is offered first), renames a key, edits its allowed origins and revokes it after a confirmation. A new key's value, and a rotation's replacement, is shown once, with a copy button, and the dialog stays open until you say you have stored it. Anyone else sees the keys and no action on them; the **New SDK key** action stays on screen, disabled, naming the roles that would allow it. The console lowercases an origin and drops its default port as the server does, and names one that is not an origin before sending.

## Status

Each listed key carries its status now:

| Status | Meaning |
|---|---|
| `active` | accepted; neither rotated nor revoked |
| `rotating` | rotated, and still accepted until its `expires_at`, the end of its rotation overlap |
| `expired` | rotated, and its overlap has ended: refused `sdk_key_expired`, without anyone acting |
| `revoked` | revoked: refused `sdk_key_revoked`, within its overlap too |

## Rotation

Rotating a key creates its replacement, of the same kind, name and allowed origins, and answers its value once. The old key keeps working for the **rotation overlap**, so you can roll the new value out to your services without an outage, then it expires by itself. The overlap is `overlap_seconds`, from 0 (the old key ends at once) to 7,776,000 (90 days); without it, 604,800 (7 days). Only an active key is rotated: a rotating, expired or revoked key is refused `409 sdk_key_not_active`, so an ended key is never brought back. The replacement takes the old key's place among the active keys, so a rotation is never refused for the limit.

## Revocation

Revoking a key ends it at once, whatever its overlap. Revoking a revoked key answers it unchanged. Use it when a value has leaked; rotate instead when you replace a key on schedule.

## Allowed origins

A client key may list the browser origins it is accepted from, at most 50. Each is an `http` or `https` origin, `scheme://host[:port]`, without a path, query or credentials, and is kept normalized: in lower case and without the scheme's default port, so `HTTPS://App.Example.com:443` is kept as `https://app.example.com`. An empty list allows any origin. A server key takes none (`server_key_origins`): it is never used from a browser, and an origin on it would only suggest a restriction that does not exist.

An update replaces the whole list and must name it, so a script that sends only a new name cannot lift a restriction by accident; send `[]` to allow any origin.

The server enforces the list, not the browser: a request from an origin the key does not list is refused `403 origin_not_allowed`, and an answer from a listed one names that origin in `Access-Control-Allow-Origin` with `Vary: Origin`. A request without an `Origin` header, which is no browser's, is served without the header. A change to the list applies on every replica within a second.

## Browsers

The SDK API answers a CORS preflight (`OPTIONS`) on any of its paths with `204` and no key, allowing `GET`, `POST` and `OPTIONS` and the headers `Authorization`, `Content-Type`, `Content-Encoding`, `Flagnetic-Schema-Version` and `Last-Event-ID`, cached for a day. Every answer, problems included, allows any origin unless a client key lists its origins, and exposes `ETag`, `Retry-After`, `RateLimit-Limit`, `RateLimit-Remaining` and `RateLimit-Reset` to scripts. Credentials are never allowed, so no cookie rides along.

A server key sent with an `Origin` header is refused `403 server_key_from_browser`, whatever the origin: a server key in a web page can be read by anyone who opens it. Use a client key there, and create a new server key if one was ever shipped to a browser.

## Audit

Every change is in the project's audit log, by the key's key ID and never with its value: `sdk_key.created`, `sdk_key.rotated` (with the replacement's identifier and key ID and the overlap), `sdk_key.updated` and `sdk_key.revoked`. A change that changes nothing, such as revoking a revoked key, records nothing. Deleting an environment or a project deletes its keys.

## Which SDKs use an environment

`GET /api/v1/projects/{project}/environments/{environment}/sdk-inventory` (`project.read`) lists the SDKs that use the environment, most recently seen first: one row per SDK name and version, platform, app version and key, with when it was first and last seen and the ruleset version it last fetched or was sent. Check it before rotating or revoking a key, or before changing a flag an old SDK version still evaluates.

Every kind of SDK request feeds it: a ruleset fetch (so an SDK that only polls appears too), a stream connect, a remote evaluation and an events batch. The SDK and its platform are those the request names (see [How an SDK names itself](#how-an-sdk-names-itself)), and a request that names none is listed as `unknown`; the app version is the context's `app_version` attribute, which only a remote evaluation carries. A row is written at most once a minute, so it may lag a minute behind, and is forgotten once its SDK has not been seen for `FLAGNETIC_SDK_INVENTORY_RETENTION_DAYS` (90 days by default). The inventory does not tell whether an SDK holds a stream open right now: each replica knows only its own streams.

A key holds at most 200 rows. A client key ships inside apps and browsers and any caller sets its own `User-Agent`, so the bound keeps whoever holds a key from growing the table without end: once a key holds 200 rows, a request that would add another is not recorded, while the rows it holds keep moving on. Each name, version, platform and app version is kept to its first 256 bytes. The sightings not recorded are counted in `flagnetic_sdk_inventory_dropped_total` (see [Operations](../operations.md#metrics)). The list is not paginated.

### How an SDK names itself

An SDK that can set `User-Agent` sends `flagnetic-<sdk>/<version> (<platform>)`, such as `flagnetic-go/1.4.0 (linux)`, on every request. A browser cannot set `User-Agent`, and `EventSource` cannot set any header, so an SDK running in one names itself in what it posts instead: a remote evaluation's context carries the standard attributes `sdk`, `sdk_version` and `platform` (`{"sdk": "flagnetic-js", "sdk_version": "1.3.0", "platform": "web"}`), and an events batch its envelope's `sdk`. A `User-Agent` naming an SDK wins over both. A request named by neither is `unknown`, and so is a ruleset fetch or a stream without such a `User-Agent`, which post nothing to name an SDK by: a browser SDK's stream is listed as `unknown`, its evaluations and events under its own name.
