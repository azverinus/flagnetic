# Events

SDKs post what they observed in batches: evaluation summaries (how often each flag served each variation, and why), aliases (the unit ID in use before sign-up linked to the one after) and exposures.

```
POST /sdk/v1/events
Authorization: Bearer fn_cli_…
Content-Type: application/json

{"schema_version": 1, "sdk": {"name": "flagnetic-go", "version": "1.4.0", "platform": "linux"},
 "sent_at": "2026-09-19T10:00:00Z",
 "events": [
   {"type": "evaluation_summary", "event_id": "01a0b91b-c500-7000-8000-000000000003",
    "interval_start": "2026-09-19T09:59:00Z", "interval_end": "2026-09-19T10:00:00Z",
    "ruleset_version": 1745040000000, "app_version": "1.18.0",
    "counts": [{"flag": "checkout", "variation": "true", "reason": "targeting_match", "count": 3}]},
   {"type": "alias", "event_id": "01a0b91b-c500-7000-8000-000000000001", "occurred_at": "2026-09-19T10:00:00Z",
    "from_unit_type": "anonymous", "from_unit_id": "d-8f1c", "to_unit_type": "user", "to_unit_id": "u-1001"}
 ]}
```

Either kind of key may post. The answer is `202` once every accepted event is durable, so the SDK can drop what it sent:

```json
{"accepted": 2, "duplicates": 0, "dropped_rows": 0, "rejected": []}
```

## The batch and each event

The batch is refused as a whole only for its envelope, its key, its size, a rate limit or a server error; the SDK retries those with the same event IDs, except a `4xx` other than `401`, `403` and `429`, which it drops.

| Limit | Refusal |
|---|---|
| At most 500 events and 1 MiB after decompression; it may be sent gzip with `Content-Encoding: gzip` | `413 request_too_large` |
| `schema_version` 1, `sdk.name` and `sdk.version` non-empty strings, `sdk.platform` a string or absent, each at most 256 bytes without U+0000, `sent_at` an RFC 3339 time, `events` a list | `400 invalid_request`, with every member at fault in `errors` |

Otherwise each event is judged on its own and a refused one is listed in `rejected` with its index, its `event_id` when it sent one as a string, a `code` and a `detail`. The SDK never sends a rejected event again. The codes are an open set a later server may add to:

| Code | When |
|---|---|
| `event_invalid` | A member is missing, of the wrong type, a string over 256 bytes or holding U+0000; a summary holds over 1,000 rows, a row counts less than 1 or names a reason that is none, or its interval starts after it ends. |
| `event_type_unknown` | The server does not know events of this `type`. |
| `event_id_invalid` | `event_id` is not a lowercase UUIDv7. |
| `out_of_window` | The time in `event_id`, or `occurred_at` (`interval_end` for a summary), is older than the retention window. |
| `undeclared_unit_type` | An alias names a unit type the project does not declare. |
| `unknown_experiment` | An exposure names no running experiment. No experiment runs yet in this version, so every exposure is refused this way. |

A summary row's `reason` is a true reason (`rule`, `rollout`, `fallthrough`, `prerequisite_failed`, `off`, `override`, `experiment`, `error`), the SDK's own `fallback` or `local_override`, or `targeting_match`, which a client SDK counts from the answers it receives. A row naming a flag the project does not have (an archived flag is still one) is dropped and counted in `dropped_rows`; the rest of its summary is stored.

An event's time more than 24 hours ahead of the server clock is taken as the time the event was received.

## Retries

Each event is stored once by its `event_id`, per project, within the retention window (`FLAGNETIC_EVENTS_RETENTION_DAYS`, 30 days by default): an event received before, in an earlier batch, on any replica and on any day, or earlier in the same batch, is counted in `duplicates`. An SDK retrying a batch therefore resends it with the same IDs.


## Evaluations answered remotely

The server counts the [remote evaluations](evaluate.md) it answers, per key, SDK, platform, app version, flag, variation and true reason (the reason before a client key's answer is masked), and writes the counts every minute, and once more when a replica stops, as evaluation summaries of its own. They are kept apart from the summaries SDKs post, so the evaluations a client SDK also counts from the answers it received are never added twice. The SDK is the one its `User-Agent` names (`flagnetic-<sdk>/<version> (<platform>)`), or `unknown`; the app version is the context's `app_version` attribute when it is a string.
