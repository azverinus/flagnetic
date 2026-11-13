# Sinks

A **sink** is a project's durable, at-least-once copy of its raw events (exposures, aliases and evaluation summaries) delivered to a target of your own, so that analysts work in their warehouse and never on the platform's database. This page is for the people who set sinks up and keep them running; the rows a sink writes, the ClickHouse tables, the object keys, the webhook envelope and a SQL recipe are on [Exported events](exported-events.md).

A project's sinks need the `sink.manage` permission, held by maintainers and owners and by project tokens with `project:admin` (see [Access](access.md)). Reading a sink needs it too, since a sink's configuration names internal hosts, buckets and URLs.

## Kinds and configuration

A sink has a name, a kind fixed when it is created, the sub-object of its kind's configuration, the `event_kinds` it exports (all three when empty) and the `environments` whose events it exports, by key (every environment when empty). Secrets are write-only: they are sealed with `FLAGNETIC_SECRET_KEY`, read back as `***`, and sending `***` or nothing keeps the stored one. Changing the target (`url`, `username` or `tls_skip_verify` of ClickHouse; `endpoint` or `access_key_id` of object storage) needs the secret again, so a stolen sink editor cannot point a stored secret at another host.

Batching is the sink's own `batch_size` (the most rows one delivery carries) and `flush_interval` (seconds the sink waits between deliveries once it has caught up; a full batch is followed by the next at once), with defaults by kind:

| Kind | `batch_size` | `flush_interval` | A call takes at most |
|---|---|---|---|
| `clickhouse` | 5,000 | 10 s | 60 s |
| `object_storage` | 500,000 | 300 s (60 to 3,600 s) | 2 min per call |
| `webhook` | 500 (at most 5,000) | 5 s | 10 s |

### ClickHouse

`clickhouse`: `url` (the HTTP interface, for example `https://clickhouse.internal:8443`, without credentials), `database`, `table_prefix` (`flagnetic` by default), `username`, `password` (secret), `tls_skip_verify` (false by default), `create_tables` (true by default) and `settings` (extra query parameters for every insert; the ones delivery sets itself are refused). The platform creates the three tables on enable when `create_tables` is true, and what they look like is on [Exported events](exported-events.md#clickhouse). A table you manage yourself may lack optional columns: that is a [warning](#warnings), not an error.

### Object storage

`object_storage`, for any S3-compatible service: `endpoint` (a scheme, a host and a port; empty for AWS), `region`, `bucket`, `prefix`, `access_key_id`, `secret_access_key` (secret), `path_style`, `format` (`jsonl` or `parquet`) and `compression` (`gzip`, `zstd` or `none` for `jsonl`; `parquet` is always zstd). Give the credentials the right to put and delete objects under the prefix: a test writes and deletes `{prefix}/_flagnetic_probe`. The key scheme is on [Exported events](exported-events.md#object-storage).

### Webhook

`webhook`: `url` and `headers` (sent with every call before the platform's own; names starting `X-Flagnetic-` are refused). Calls are signed as [webhook subscriptions are](webhooks.md#sending), with a secret the server draws: it is shown once, in the response that creates the sink, and never again. `POST …/sinks/{sink}/rotate-secret` draws a new one; the old one keeps signing beside it for `FLAGNETIC_WEBHOOK_SECRET_OVERLAP`, so the receiver can switch without refusing a call. The body is the envelope on [Exported events](exported-events.md#webhook).

## Guards

The platform makes the calls, so a sink could be pointed at the network the instance runs in. A target is refused when it is written, and again at every connection, by the address its name resolves to.

- A **webhook sink** is held to the same policy as webhook subscriptions: `https` unless `FLAGNETIC_WEBHOOK_ALLOW_HTTP`, and nothing outside the public internet unless `FLAGNETIC_WEBHOOK_ALLOW_PRIVATE_NETWORKS`.
- A **ClickHouse or object storage sink** may reach loopback and private addresses, because a warehouse usually sits in the cluster's own network, but never a link-local address (where cloud metadata services answer); no setting lifts that.
- No sink follows a redirect, and no unspecified or multicast address is ever called.
- A target's own words never reach a reader: not in the API, not in the console, not in the logs, not in an audit entry or a webhook event. A failure is told by its **failure class**, the HTTP status when the target answered and the protocol's own error code when it speaks one (a ClickHouse exception code, an S3 error code).

## Testing, enabling and sampling

- `POST …/sinks/test` tests a configuration as you would create it, and `POST …/sinks/{sink}/test` tests a stored sink, optionally with the sub-object of its kind in the body, which replaces the stored configuration for the test only. A test answers `ok`, and when not, the failure class, status and code; `warnings` list what is wrong without stopping delivery. A test of an unsaved configuration stores nothing and creates no table. Saving a sink is never blocked by a test.
- `POST …/sinks/{sink}/enable` runs the same check and refuses with `sink_not_ready`, carrying the failure, when the target cannot take a batch. A sink is created disabled. Enabling a webhook sink also calls the receiver once with the `test` envelope.
- `start_from` on enable sets where the streams this call creates start: absent, at the moment of enabling; otherwise the first event received at or after that time, within `FLAGNETIC_EVENTS_RETENTION_DAYS`. A stream the sink already has keeps its place, so disabling and enabling again continues where it stopped. Events received before the sink was enabled are not exported unless you ask for them with `start_from` or a [replay](#replay).
- `POST …/sinks/{sink}/sample` sends ten synthetic rows of each kind through the normal path without moving a cursor, to a disabled sink too, and answers per kind whether the target took them. Use it to see the tables or files and to build a dashboard before real data arrives; the rows are named in [Exported events](exported-events.md#sample-rows).

## Health

`GET …/sinks/{sink}` embeds one **sink health**, first match wins:

| Health | Meaning |
|---|---|
| `disabled` | A member turned the sink off. Cursors hold; a batch already in flight is still resent until the target acknowledges it. |
| `stopped` | The platform stopped it after `FLAGNETIC_SINK_STOP_AFTER_DEAD_LETTERS` consecutive dead letters on one stream: a misconfiguration, not a bad batch. A member must resume it. |
| `failing` | A stream's current batch is being retried. |
| `degraded` | Delivery works, but dead letters are waiting to be redelivered or discarded. |
| `lagging` | Delivering, but the oldest undelivered row is older than `FLAGNETIC_SINK_LAG_WARNING`. |
| `healthy` | None of the above, including an enabled sink that has had nothing to deliver yet. |

Alongside it: `at_risk`, `lag_seconds` (0 while every stream has read every row its filter admits; null while disabled), the pending `dead_letters` count, the undismissed `gaps` count, the `warnings` and, per event kind, the time of the last delivery and the last failure. Entering `failing`, `stopped` or `at_risk`, and recording a gap, tell the project's [webhook subscriptions](webhooks.md#sinks) once.

## Failure classes

Every failed call has one class, from a closed list:

| Class | Meaning | Retried |
|---|---|---|
| `dns` | The name did not resolve | without limit |
| `connect` | The target could not be reached | without limit |
| `tls` | The connection could not be secured | without limit |
| `timeout` | The target did not answer in time | without limit |
| `status` | The target answered a status that says it is busy or broken (408, 429, 5xx) | without limit |
| `refused_target` | The address is one a sink of this kind may not reach; no call was made | without limit, since what a name resolves to can change |
| `redirect` | The target answered with a redirect, which no sink follows | without limit |
| `rejected` | The target understood the batch and refuses it: any other 4xx, or a ClickHouse exception saying the table or the data does not fit | `FLAGNETIC_SINK_REJECTION_ATTEMPTS` times, then a dead letter |

A transient failure is retried with the same batch under the same delivery ID, after a second, doubling to five minutes, each wait moved by up to 20 % either way; the batch keeps its place and nothing behind it is sent. Only a rejection is counted: it is retried `FLAGNETIC_SINK_REJECTION_ATTEMPTS` times, then the batch becomes a dead letter.

## Dead letters, stopping and resume

A **dead letter** records a batch set aside after its rejections ran out: the event kind, the cursor range, the environments filter, the attempts and the last failure, never the rows, which stay in PostgreSQL until retention drops them. The stream goes on with the next batch.

- `GET …/sinks/{sink}/dead-letters` lists them. `POST …/dead-letters/{deadLetter}/redeliver` reads the range again and sends it inside the request, under a delivery ID of its own for each attempt; the dead letter becomes `redelivered` only when the target acknowledges, so a failed redelivery leaves it pending and can be repeated. It is refused `422 rows_expired` when retention has dropped every row of it. `DELETE …/dead-letters/{deadLetter}` discards it.
- A pending dead letter none of whose rows is stored any more is marked `expired` by retention.
- After `FLAGNETIC_SINK_STOP_AFTER_DEAD_LETTERS` dead letters in a row on one stream (a batch the target acknowledges ends the run) the sink **stops**: a target that keeps refusing is not fed the rest of the event store. Nothing is sent, no cursor moves, and the project's subscriptions are told `sink.stopped`. Find out why from the last failure, fix the target or the configuration, and `POST …/sinks/{sink}/resume`. Resuming does not redeliver dead letters; do that yourself.

## Replay

`POST …/sinks/{sink}/replay` with `from`, optionally `to` and `event_kinds` sends events again by the time they were received, to fill a new table, recover from a dead letter whose rows have gone, or load history after widening the environments filter. `from` must be within retention. Without `to` the replay runs on into the live stream; with it, the stream returns to where it was when the replay ends. Replayed rows are sent under their own delivery IDs, so a target that deduplicates on the delivery ID sees them as new; deduplicate on the row IDs, as [Exported events](exported-events.md#delivery-guarantees) says. A replay is also the way to deliver events received before a sink was first enabled.

## At risk and gaps

Raw events live in PostgreSQL for `FLAGNETIC_EVENTS_RETENTION_DAYS`. A sink that cannot deliver, because its target is down, it is disabled or stopped, or a long transaction holds it back, falls behind, and retention would drop what it has not delivered. The platform shows it before it happens and records it when it does:

- A sink is **at risk** when one of its streams has an undelivered row within `FLAGNETIC_EVENTS_LOSS_WARNING_HOURS` of the edge of retention, disabled and stopped sinks included. `sink.at_risk` is told once on entering. Recovering the target, resuming the sink or raising `FLAGNETIC_EVENTS_RETENTION_DAYS` clears it.
- When retention drops rows a sink had not delivered anyway, each stream that lost rows gets a **gap**: the rows lost, when they were received and when they were dropped. The cursor moves to the first row still stored and the stream goes on; `sink.data_lost` is told once for each gap. `GET …/sinks/{sink}/gaps` lists them and `POST …/gaps/{gap}/dismiss` marks one as seen, which keeps it listed and takes it out of the sink's `gaps` count. Rows a gap lost can no longer be replayed; load them from another source.

[Operations](operations.md#raw-events) describes the drop itself.

### A long transaction holds streams back

A stream reads a row only once every transaction that could still commit a row before it has ended, so that a row committing late is never skipped. Any transaction left open in the database, by Flagnetic or by anything else connected to it, holds every sink's streams back until it ends: ingest goes on, the rows are kept, and `lag_seconds` and `flagnetic_sink_lag_seconds` grow in step for every sink. [Operations](operations.md#a-long-transaction-holds-the-rollups-back) shows how to find the transaction. A lag that grows toward the retention edge makes every sink at risk.

## Deleting an environment

A sink's rows carry the key of their environment, which is gone when the environment is deleted, so rows of a deleted environment are not exported: a stream passes them, and `flagnetic_sink_rows_skipped_total{reason="environment_deleted"}` counts them. Rows a sink has not yet delivered are lost with the environment. **Let the sinks catch up before deleting an environment**: wait until `lag_seconds` is 0 on every sink that exports it. A sink that names environments of which none exists any more exports nothing until it is edited.

## Warnings

A **sink warning** is what the last test or enable found that does not stop delivery. For ClickHouse it is an optional column of the export schema a table lacks, which inserts skip. The next test or enable replaces them all. A missing table, or a table without a required column, is not a warning but a failure of the test, the enable and the insert, with the code `60` (`UNKNOWN_TABLE`) or `16` (`NO_SUCH_COLUMN_IN_TABLE`).

## Metrics

| Metric | Labels | Meaning |
|---|---|---|
| `flagnetic_sink_batches_total` | `sink`, `event_kind`, `result` | Batches attempted, by `ack`, `transient`, `rejected`, `dead_lettered`, `redelivered` or `redelivery_failed` |
| `flagnetic_sink_lag_seconds` | `sink`, `event_kind` | Age of the oldest undelivered row of a stream, 0 when caught up |
| `flagnetic_sink_dead_letters` | `sink` | Dead letters waiting to be redelivered or discarded |
| `flagnetic_sink_at_risk` | `sink` | 1 while the sink is at risk, 0 otherwise |
| `flagnetic_sink_rows_skipped_total` | `reason` | Rows passed without being exported: `environment_deleted` |

The `sink` label is the sink's ID. [Operations](operations.md#metrics) lists them with every other metric. Worth an alert: any `flagnetic_sink_at_risk` at 1, `flagnetic_sink_lag_seconds` above the lag warning for several minutes, and `flagnetic_sink_dead_letters` above 0.

## Settings

| Setting | Meaning |
|---|---|
| `FLAGNETIC_SINK_TICK` | How often a replica looks for streams due for a batch |
| `FLAGNETIC_SINK_LAG_WARNING` | Lag above which a delivering sink is `lagging` |
| `FLAGNETIC_SINK_REJECTION_ATTEMPTS` | Rejections of one batch before it becomes a dead letter |
| `FLAGNETIC_SINK_STOP_AFTER_DEAD_LETTERS` | Consecutive dead letters on one stream that stop the sink |
| `FLAGNETIC_EVENTS_RETENTION_DAYS` | Days raw events stay in PostgreSQL: how far a sink can fall behind and a replay reach back |
| `FLAGNETIC_EVENTS_LOSS_WARNING_HOURS` | How close to the retention edge an undelivered row makes a sink at risk |
| `FLAGNETIC_WEBHOOK_ALLOW_HTTP`, `FLAGNETIC_WEBHOOK_ALLOW_PRIVATE_NETWORKS` | What a webhook sink's target may be |
| `FLAGNETIC_WEBHOOK_SECRET_OVERLAP` | How long a rotated webhook secret keeps signing |
| `FLAGNETIC_SECRET_KEY` | Seals every sink's secret fields |

Defaults and units are in the [configuration reference](configuration.md).
