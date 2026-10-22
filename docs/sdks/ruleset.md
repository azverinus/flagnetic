# Downloading the ruleset

A server SDK downloads its environment's whole ruleset, private segments included, and evaluates every flag locally.

```
GET /sdk/v1/ruleset
Authorization: Bearer fn_srv_…
```

Only a server key receives a ruleset; a client key is refused `403 key_kind_not_allowed` and asks the server to evaluate for it instead.

## Versions and conditional requests

The answer's `ETag` is the ruleset's version, quoted, as a strong tag: `ETag: "1745040000000"`. Send it back in `If-None-Match` and an unchanged ruleset is answered `304` with no body, so polling an environment nobody changed costs almost nothing. `If-None-Match` is compared weakly, so a `W/` tag a proxy made of the strong one still matches; it may list several tags, and `*` matches any version.

Every answer carries `Cache-Control: private, no-cache` (no shared cache may keep it, and an SDK revalidates it each time) and `Vary: Authorization, Accept-Encoding`.

## Changes and replicas

Every replica keeps each environment's ruleset in memory by version and learns every change through a PostgreSQL notification sent when the change commits, so any replica can serve any SDK and none needs sticky sessions. A change made through a replica is served by that same replica as soon as the write is answered, and by every other replica once its notification arrives, normally well within a second. The ruleset is built from the database once per environment however many SDKs ask at the same moment.

A replica whose notification listener is down keeps no ruleset in memory and builds it from the database for every request, since it could have missed a change. When its listener connects again, as after a database failover or a restore, it forgets everything it held and takes the versions the database holds as the newest, even when they are older than versions it saw before: an SDK is never kept waiting for, or served, a version the database lost. Versions come from the server clock, so the first change after such a recovery is numbered above every version handed out before it, and reaches every SDK.

While a replica cannot build a ruleset for two seconds (the database slow or unreachable), it answers `503 ruleset_unavailable` with `Retry-After: 2` rather than an empty ruleset: the SDK keeps the ruleset it holds and asks again. The build goes on in the background for up to 30 seconds, so the requests after it find it done.

## Downloading from the management API

`GET /api/v1/projects/{project}/environments/{environment}/ruleset` answers the same document a server key receives, with the same `ETag`, to anyone holding `project.read` in the project: a file to feed a test helper or to evaluate offline. It carries the replica's own latest writes and answers `503 ruleset_unavailable` with `Retry-After` the same way.

## Compression

An SDK sending `Accept-Encoding: gzip` receives the ruleset in gzip. Every answer of the SDK API is compressed the same way, problems included, and every answer varies by `Accept-Encoding`. A request body may be sent in gzip with `Content-Encoding: gzip`; every size limit counts the body after decompression, a body that is not valid gzip is `400 invalid_request`, and any other coding `415 unsupported_media_type`.

## Schema versions

`Flagnetic-Schema-Version` names the highest document schema the SDK reads. Absent, or any whole number from 1 up, is served schema 1, the only one there is; a value below 1 or anything else (`one`, `1.5`, the header sent twice) is refused `406 schema_version_unsupported`.

## Errors

Every error is a problem document (`application/problem+json`) in the same shape as the management API's. A path the SDK API does not serve is `404 not_found`; a method a path does not accept `405 method_not_allowed`, with `Allow`. The answers to a key that does not work are in [SDK keys](keys.md#presenting-a-key).
