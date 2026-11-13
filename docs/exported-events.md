# Exported events

A [sink](sinks.md) gives a project's analysts a durable copy of its events without any access to the platform's database: ClickHouse over its HTTP interface, S3-compatible object storage as JSON Lines or Parquet files, or a signed webhook. This page is the contract every sink kind keeps: the **export schema** and its compatibility promise, what each sink kind writes, and a SQL recipe that reproduces the experiment page's first exposures, sample-ratio check and buckets from the exported rows. How a sink is configured, guarded, recovered and monitored is on [Sinks](sinks.md).

Three event kinds are exported, each through its own **stream** (one cursor per sink and event kind):

- `exposure`: one row per exposure an SDK sent, override exposures included and marked.
- `alias`: one row per alias, a recorded link between two unit IDs, typically an anonymous ID to a user ID after sign-up.
- `evaluation_summary`: one row per flag, variation and evaluation reason of an evaluation summary, whether an SDK reported it or the server counted the remote evaluations it answered.

A sink selects some of the three (`event_kinds`, all by default) and may be limited to some environments (`environments`, all by default).

## Delivery guarantees

- **At least once.** A stream's cursor moves only after the target acknowledges a batch, so a crash, a lost lease or a retry can send the same rows again, and a replay sends them again on purpose. Deduplicate by `event_id` (a summary row by `report_id` with its flag, variation and evaluation reason). The platform itself never stores an event twice: an SDK's retry of an event ID it already holds is dropped on receipt.
- **The same rows, the same delivery ID.** Every batch is sent under a `delivery_id` (`dlv_` and 26 hex digits), a hash of the sink, the event kind, the cursor range and the ordered IDs of the rows the batch holds. Every attempt of a batch carries the same ID; any other set of rows carries another, even over the same range. It is the webhook's `X-Flagnetic-Delivery`, the ClickHouse `query_id` and the hash in an object's key. Three sends are outside this rule: redelivering a dead letter is sent under an ID of its own for each attempt, a sample is sent as `sample_` and a UUID, and a webhook test as `test_` and a UUID. Deduplicate rows by their IDs, not only by the delivery ID.
- **Ordered within a stream.** Rows arrive in the platform's storage order: by the transaction that stored them, then by row. That is close to, but not, `received_at` order. Streams of one sink are independent: aliases may arrive before the exposures they refer to.
- **Nothing lost silently.** A sink that is down keeps its cursor while rows wait in PostgreSQL for `FLAGNETIC_EVENTS_RETENTION_DAYS`. The sink is **at risk** when an undelivered row nears the retention edge, and a **gap** is recorded if retention drops rows the sink never received. See [Sinks](sinks.md#at-risk-and-gaps).

## The export schema

Every sink kind exports the same flat, snake_case rows, `schema_version` 1.

- **The compatibility promise.** The export schema, the ClickHouse DDL, the object key scheme and the webhook envelope change only additively. Within one `schema_version` a column is never renamed, retyped or removed, and its meaning does not change. New columns are appended after the existing ones, so a consumer that has not adopted them reads the rows as before; a ClickHouse table you created yourself needs no change, since inserts send only the columns it has. Consumers ignore columns they do not know.
- A change that is not additive, a rename, a type change or a removal, arrives as a new `schema_version`, exported beside the old one, with a migration note for platform-created ClickHouse tables. Rows of the new version carry it, so a consumer can tell them apart.
- The current columns are never null. A string the SDK did not send (a platform, an app version, the variation of a summary row that served none) is exported as `''`.
- Never exported: the platform's internal IDs, the position of a row in storage, SDK key IDs and salts. Read salts and weights from the experiment page or `GET /api/v1/projects/{project}/experiments/{experiment}`.

Types: `string` is UTF-8 text; `bool` is true or false; `int` and `int64` are integers; `timestamp` is an instant with millisecond precision in UTC. How each sink kind encodes them is under [Encodings](#encodings).

### Exposure row

<!-- columns:exposure -->
| Column | Type | Meaning |
|---|---|---|
| `schema_version` | int | Row schema version, `1` |
| `event_id` | string | The UUIDv7 the SDK chose for the exposure; the deduplication key |
| `project_key` | string | The project |
| `environment_key` | string | The environment whose SDK key sent it |
| `experiment_key` | string | The experiment |
| `flag_key` | string | The experiment's flag |
| `variation_key` | string | The variation served |
| `unit_type` | string | The experiment's unit type |
| `unit_id` | string | The unit ID the assignment was hashed from |
| `evaluation_reason` | string | `experiment`, or `override` when an override decided the flag |
| `override` | bool | True when an override decided the variation, on the flag or on a prerequisite; such a row is a QA session and never a first exposure |
| `platform` | string | The context's platform, may be empty |
| `app_version` | string | The context's app version, may be empty |
| `sdk_name` | string | The SDK that sent it |
| `sdk_version` | string | Its version |
| `ruleset_version` | int64 | The ruleset version the SDK evaluated against |
| `occurred_at` | timestamp | When the unit was exposed, by the SDK's clock |
| `received_at` | timestamp | When the platform stored it, by the server's clock |

### Alias row

<!-- columns:alias -->
| Column | Type | Meaning |
|---|---|---|
| `schema_version` | int | Row schema version, `1` |
| `event_id` | string | The UUIDv7 the SDK chose for the alias; the deduplication key |
| `project_key` | string | The project |
| `environment_key` | string | The environment whose SDK key sent it |
| `from_unit_type` | string | The unit type in use before the link, typically anonymous |
| `from_unit_id` | string | The unit ID in use before the link |
| `to_unit_type` | string | The unit type after the link, typically user |
| `to_unit_id` | string | The unit ID after the link |
| `platform` | string | The sending SDK's platform, may be empty |
| `sdk_name` | string | The SDK that sent it |
| `sdk_version` | string | Its version |
| `occurred_at` | timestamp | When the SDK recorded the link, by its clock |
| `received_at` | timestamp | When the platform stored it, by the server's clock |

### Evaluation-summary row

One summary becomes one row per flag, variation and evaluation reason it counted. Counts are at least once and lossy at the edges: use them to tell whether and where a flag is still evaluated, never to analyse an experiment, which is what exposures are for.

<!-- columns:evaluation_summary -->
| Column | Type | Meaning |
|---|---|---|
| `schema_version` | int | Row schema version, `1` |
| `report_id` | string | The ID of the report the row belongs to, shared by every row of an SDK report; with `flag_key`, `variation_key` and `evaluation_reason`, the deduplication key. See [Report IDs](#report-ids) |
| `project_key` | string | The project |
| `environment_key` | string | The environment whose SDK key sent it |
| `flag_key` | string | The flag evaluated |
| `variation_key` | string | The variation served, empty when none was |
| `evaluation_reason` | string | Why it was served |
| `count` | int64 | Evaluations in the window, at least 1 |
| `window_start` | timestamp | Start of the reporting window, by the SDK's clock |
| `window_end` | timestamp | End of the reporting window, by the SDK's clock |
| `sdk_name` | string | The SDK that reported |
| `sdk_version` | string | Its version |
| `platform` | string | The SDK's platform, may be empty |
| `app_version` | string | The app version, may be empty |
| `ruleset_version` | int64 | The ruleset version evaluated against |
| `received_at` | timestamp | When the platform stored it, by the server's clock |
| `source` | string | Who counted: `sdk` for what an SDK evaluated itself, `remote_evaluation` for what the server answered. Appended after the first release's columns |

`source` separates what SDKs read from their rulesets from the evaluations the server performed for client SDKs, which are counted under the SDK that asked. A client SDK is sent every flag whether the app reads it or not, so add up `remote_evaluation` rows separately from `sdk` rows.

#### Report IDs

An SDK's report ID is the event ID it chose for the report. The server has no report ID for the evaluations it answered itself, so the row's `report_id` is a UUID derived from the row's place in storage: a version 5 (name-based, SHA-1) UUID in the namespace `7d4f8c1e-2a6b-4e5d-9c3f-0b1a8e6d5c42`, over the name `{transaction}:{sequence}` of the stored row, both numbers in decimal. The stored row never changes, so a retry and a replay export the same ID, and two rows of the same flag, variation and reason are never folded into one by a table that deduplicates on the report ID. The two numbers are not exported; treat the ID as opaque.

## Encodings

| Type | JSON (webhook, JSON Lines) | ClickHouse | Parquet |
|---|---|---|---|
| string | string | `String` or `LowCardinality(String)` | `BYTE_ARRAY` annotated `STRING` (UTF8) |
| bool | `true` or `false` | `Bool` | `BOOLEAN` |
| int | number | `UInt16` | `INT64` annotated `INT(64, signed)` |
| int64 | number | `UInt64` | `INT64` annotated `INT(64, signed)` |
| timestamp | number: Unix milliseconds | `DateTime64(3, 'UTC')` | `INT64` annotated `TIMESTAMP(MILLIS, UTC)` |

In JSON a timestamp is an integer count of milliseconds since the Unix epoch, for example `"occurred_at": 1758704400123`, never a string. ClickHouse is sent the same instant as an RFC 3339 string in UTC with milliseconds, so a `DateTime64` column in any time zone stores the right instant. Text is written as it was sent, with no HTML escaping.

## ClickHouse

The tables are `{table_prefix}_exposures`, `{table_prefix}_aliases` and `{table_prefix}_evaluation_summaries` (`flagnetic` by default) in the sink's database. On enable, and on a test of the stored sink, the platform runs `DESCRIBE TABLE` on each selected kind's table. A missing table is created from the DDL below when `create_tables` is true; otherwise the action fails, the failure naming the table in the warnings. An unsaved test never creates anything and says so. Delivery also creates a table that has gone missing when `create_tables` is true.

- A table you create yourself (`create_tables: false`) may use any engine, ordering, partitioning and TTL, and may omit columns: inserts send only the columns the table has, found by a `DESCRIBE` before every insert. An omitted column is a [sink warning](sinks.md#warnings), not an error, so you adopt new export columns at your own pace.
- `schema_version`, the row's ID (`event_id`, or a summary's `report_id`), `project_key`, `environment_key` and `received_at` are required: a table without one of them fails the test and the enable.
- An insert is `INSERT INTO {database}.{table} ({columns}) FORMAT JSONEachRow`, gzipped, with `async_insert=0` (a 200 means the rows are written), `date_time_input_format=best_effort` and `query_id` set to the batch's delivery ID. The sink's `settings` are added as query parameters; the ones delivery sets itself are refused. The call takes 60 seconds at most.
- No `insert_deduplication_token` is sent: a table converges on its sorting key instead.

The platform-created tables are `ReplacingMergeTree`, partitioned by month of `received_at`, with no TTL: retention in ClickHouse is yours. Their `ORDER BY` is also their deduplication key, so it ends in exactly the columns that make a row unique, and a retried or replayed row folds into the one already stored at the next merge. Until that merge, duplicates are visible without `FINAL`; the recipe below never needs `FINAL`.

<!-- ddl:exposure -->
```sql
CREATE TABLE flagnetic_exposures (
  schema_version UInt16,
  event_id String,
  project_key LowCardinality(String),
  environment_key LowCardinality(String),
  experiment_key LowCardinality(String),
  flag_key LowCardinality(String),
  variation_key LowCardinality(String),
  unit_type LowCardinality(String),
  unit_id String,
  evaluation_reason LowCardinality(String),
  override Bool,
  platform LowCardinality(String),
  app_version String,
  sdk_name LowCardinality(String),
  sdk_version LowCardinality(String),
  ruleset_version UInt64,
  occurred_at DateTime64(3, 'UTC'),
  received_at DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(received_at)
ORDER BY (project_key, environment_key, experiment_key, unit_id, event_id);
```

<!-- ddl:alias -->
```sql
CREATE TABLE flagnetic_aliases (
  schema_version UInt16,
  event_id String,
  project_key LowCardinality(String),
  environment_key LowCardinality(String),
  from_unit_type LowCardinality(String),
  from_unit_id String,
  to_unit_type LowCardinality(String),
  to_unit_id String,
  platform LowCardinality(String),
  sdk_name LowCardinality(String),
  sdk_version LowCardinality(String),
  occurred_at DateTime64(3, 'UTC'),
  received_at DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(received_at)
ORDER BY (project_key, environment_key, from_unit_type, from_unit_id, event_id);
```

<!-- ddl:evaluation_summary -->
```sql
CREATE TABLE flagnetic_evaluation_summaries (
  schema_version UInt16,
  report_id String,
  project_key LowCardinality(String),
  environment_key LowCardinality(String),
  flag_key LowCardinality(String),
  variation_key LowCardinality(String),
  evaluation_reason LowCardinality(String),
  count UInt64,
  window_start DateTime64(3, 'UTC'),
  window_end DateTime64(3, 'UTC'),
  sdk_name LowCardinality(String),
  sdk_version LowCardinality(String),
  platform LowCardinality(String),
  app_version String,
  ruleset_version UInt64,
  received_at DateTime64(3, 'UTC'),
  source LowCardinality(String)
) ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(received_at)
ORDER BY (project_key, environment_key, flag_key, report_id, variation_key, evaluation_reason);
```

A summary table ordered by `report_id` alone would fold every row of a report but one away at its first merge; if you create the table yourself, keep the three columns after `report_id`.

## Object storage

A batch is one **batch file**, written every `flush_interval` or at `batch_size` rows, whichever comes first, with one PUT; a successful PUT is the acknowledgement. The object key is

```
{prefix}/{event_kind}/dt={YYYY-MM-DD}/{project_key}-{first_seq}-{last_seq}-{batch_hash}.{extension}
```

- `event_kind` is `exposure`, `alias` or `evaluation_summary`.
- `dt` is the UTC day of the file's first row's `received_at`. Hive-style, so Athena and Spark read it as a partition column, and so does ClickHouse `s3()` with `use_hive_partitioning = 1`.
- `first_seq` and `last_seq` are the positions in storage of the file's first and last row, which order a stream's files.
- `batch_hash` is the batch's delivery ID without its `dlv_` prefix, 26 hex digits. The same rows always land at the same key, so a retry overwrites its own file with identical rows. Any other set of rows lands in another file beside it, even over the same range: a replay under a narrower filter writes new, overlapping files, and consumers deduplicate by `event_id`. The platform never overwrites a file with different rows.
- `extension` and the content type follow the format: `jsonl.gz` (`application/gzip`), `jsonl.zst` (`application/zstd`), `jsonl` (`application/x-ndjson`) or `parquet` (`application/vnd.apache.parquet`).

A JSON Lines file holds one row per line, encoded as under [Encodings](#encodings). A Parquet file is always zstd-compressed, has one row group per 100,000 rows and the types in the table above. A test writes and deletes `{prefix}/_flagnetic_probe`; a sample writes `{project_key}-sample-{YYYYMMDDTHHMMSSZ}-{hash}` files in the same partitions. A batch with no rows writes no file.

Reading a bucket's exposures with ClickHouse:

```sql
SELECT *
FROM s3('https://bucket.s3.eu-central-1.amazonaws.com/flagnetic/exposure/dt=*/*.parquet', 'Parquet')
WHERE dt >= '2026-09-01'
SETTINGS use_hive_partitioning = 1;
```

## Webhook

Each call is a `POST` of `application/json` carrying one event kind. The body is the envelope:

```json
{
  "schema_version": 1,
  "sink_id": "0197c1d2-5b3e-7a41-9c0f-3d2e8a6b4f10",
  "project_key": "shop",
  "delivery_id": "dlv_3f1c9a0b7e2d4c6a8b0e1f2d3c",
  "event_kind": "exposure",
  "sample": false,
  "events": [ { "schema_version": 1, "event_id": "0192…", … } ]
}
```

`events` holds rows of the export schema. `sample` is true for the sink's send-sample action, whose `delivery_id` is `sample_` and a UUID. A test sends `"event_kind": "test"` with an empty `events` and a `delivery_id` of `test_` and a UUID. The envelope changes only additively, as the rows do.

Headers: the sink's own `headers` first, then the platform's:

- `X-Flagnetic-Delivery`: the `delivery_id`, the same on every attempt of the same rows. Deduplicate on it or on the rows' IDs.
- `X-Flagnetic-Timestamp`: Unix seconds at send time.
- `X-Flagnetic-Signature`: `v1=` followed by the lower-case hex of HMAC-SHA256 keyed by the sink's secret over `{timestamp}.{raw body}`; while a rotated secret still signs, one such value per secret, comma separated. This is the platform's one signing scheme, shared with webhook subscriptions: [Sending](webhooks.md#sending) has a verification function in Go, Python and JavaScript, and it applies as written. The secret is the one shown when the sink was created; [Sinks](sinks.md#webhook) says how to rotate it.

A 2xx acknowledges the batch. 408, 429, 5xx, a redirect (which is never followed), a timeout and a transport error are transient and retried without limit. Any other status is a **rejection**: retried a bounded number of times, then the batch becomes a dead letter. A call takes 10 seconds at most.

## Sample rows

A sink's send-sample action writes ten synthetic rows of each selected kind through the normal path without moving any cursor: `event_id` (or `report_id`) `sample-1` to `sample-10`, `environment_key`, `experiment_key`, `unit_id` and the other keys and identities set to `sample`, `evaluation_reason` `experiment` and `source` `sdk`. The `project_key` is the project's real key. They land in the same tables, partitions and endpoints as real rows. Leave them out with `environment_key != 'sample'` or `NOT startsWith(event_id, 'sample-')`; the recipe below never selects them, since it filters on a real environment and experiment.

## SQL recipe: first exposures, sample-ratio check and buckets

This reproduces, from a ClickHouse sink's tables, what the experiment page shows: first exposures per variation, the sample-ratio check's statistic ([the sample-ratio check](sample-ratio-check.md)) and each unit's buckets. The queries are written against the platform-created tables and are duplicate-safe: retries and replays leave extra copies of rows until ClickHouse merges them, and none of the steps needs `FINAL`.

Inputs, all on the experiment page or `GET /api/v1/projects/{project}/experiments/{experiment}`:

- the project key (`shop` below), the environment key (`production`) and the experiment key (`checkout_v2`);
- the declared weights, in basis points summing to 10000 (`control` 5000, `treatment` 5000), in the experiment's variation order;
- the inclusion salt (`a3f9c2e17b4d0e58`), the variation salt (`7e1d4c90b2a6f3d1`) and the traffic allocation (10000 below: every unit included).

### Step 1: first exposures

A unit's **first exposure** is its non-override exposure with the earliest `occurred_at`, ties broken by `event_id`, whatever order the rows arrived in. A copy of a row never changes a minimum, so duplicates are harmless. Override exposures (`override = true`, QA sessions) are left out, as the platform leaves them out.

<!-- recipe:first-exposures -->
```sql
CREATE OR REPLACE VIEW exp_first_exposures AS
SELECT project_key, environment_key, experiment_key, unit_type, unit_id,
       argMin(variation_key, (occurred_at, event_id)) AS variation_key,
       min(occurred_at) AS first_occurred_at
FROM flagnetic_exposures
WHERE override = false
GROUP BY project_key, environment_key, experiment_key, unit_type, unit_id;
```

### Step 2: observed split and the sample-ratio check

`N` is the total of first exposures in the experiment's variations, `E_i = N × w_i / 10000` and `X² = Σ (O_i − E_i)² / E_i` with `k − 1` degrees of freedom. The platform alerts at p < 0.001 and skips the check when `N < 1000` or some `E_i < 5`; [the sample-ratio check](sample-ratio-check.md) has the thresholds and the p-value. The query lists each weighed variation with its `observed` first exposures, its `expected` count and the `chi_square` of the experiment on every row.

<!-- recipe:sample-ratio -->
```sql
WITH weights AS (
       SELECT * FROM values('variation_key String, weight Float64',
                            ('control', 5000), ('treatment', 5000))
     ),
     counts AS (
       SELECT variation_key, count() AS n
       FROM exp_first_exposures
       WHERE project_key = 'shop' AND environment_key = 'production' AND experiment_key = 'checkout_v2'
       GROUP BY variation_key
     ),
     total AS (
       SELECT sum(n) AS n FROM counts WHERE variation_key IN (SELECT variation_key FROM weights)
     )
SELECT w.variation_key AS variation_key,
       c.n AS observed,
       (SELECT n FROM total) * w.weight / 10000 AS expected,
       sum(pow(observed - expected, 2) / expected) OVER () AS chi_square
FROM weights AS w
LEFT JOIN counts AS c ON c.variation_key = w.variation_key
ORDER BY w.variation_key;
```

`observed` and `expected` per variation divided by `N` are the page's observed and expected shares.

### Step 3: buckets

`bucket = murmurHash3_32('{salt}.{unit_id}') % 10000`: MurmurHash3 x86 32-bit with its seed parameter at 0, over the UTF-8 bytes, read unsigned, exactly ClickHouse's `murmurHash3_32`. A unit is included when its inclusion bucket is below the traffic allocation, and assigned the variation whose cumulative weight range, in the experiment's variation order, holds its variation bucket: here control is [0, 5000) and treatment [5000, 10000). A row whose `assigned` differs from its `variation_key` points at an SDK evaluating differently from the platform, or at an unstable unit ID. The experiment's assignment tool, `GET /api/v1/projects/{project}/experiments/{experiment}/assignment?unit_id=`, shows the same buckets for one unit.

<!-- recipe:buckets -->
```sql
SELECT unit_id, variation_key,
       murmurHash3_32(concat('a3f9c2e17b4d0e58', '.', unit_id)) % 10000 AS inclusion_bucket,
       inclusion_bucket < 10000 AS included,
       murmurHash3_32(concat('7e1d4c90b2a6f3d1', '.', unit_id)) % 10000 AS variation_bucket,
       multiIf(variation_bucket < 5000, 'control', 'treatment') AS assigned
FROM exp_first_exposures
WHERE project_key = 'shop' AND environment_key = 'production' AND experiment_key = 'checkout_v2';
```

### Step 4: from anonymous units to users through aliases

An experiment keyed on anonymous units reaches the user each became through the aliases the SDK recorded at sign-up. `DISTINCT` collapses copies of an alias row; a unit linked to several users yields one row per user, and choosing one is the analysis's decision, not the platform's.

<!-- recipe:aliases -->
```sql
SELECT f.unit_id AS unit_id, f.variation_key AS variation_key,
       a.to_unit_type AS user_unit_type, a.to_unit_id AS user_id
FROM exp_first_exposures AS f
INNER JOIN (
  SELECT DISTINCT project_key, environment_key, from_unit_type, from_unit_id, to_unit_type, to_unit_id
  FROM flagnetic_aliases
) AS a
  ON a.project_key = f.project_key AND a.environment_key = f.environment_key
 AND a.from_unit_type = f.unit_type AND a.from_unit_id = f.unit_id
WHERE f.project_key = 'shop' AND f.environment_key = 'production' AND f.experiment_key = 'checkout_v2';
```

Join `user_id` to your conversions to attribute them to the variation the anonymous unit was first exposed to.
