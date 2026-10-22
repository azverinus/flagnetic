# Remote evaluation

An SDK that holds no ruleset, such as a browser or a mobile app with a client key, posts a context and receives the served value of every flag.

```
POST /sdk/v1/evaluate
Authorization: Bearer fn_cli_…
Content-Type: application/json

{"context": {"units": {"user": "u-1001"}, "attributes": {"country": "nz", "app_version": "2.4.0"}},
 "flags": ["checkout", "banner_text"]}
```

Either kind of key may ask. The server evaluates its environment's whole ruleset, private segments included, which never leaves it. `flags` names the flags to evaluate; without it every flag is evaluated, and a flag named that the ruleset does not hold is left out of the answer, so an old app asking for a deleted flag still gets the others.

```json
{
  "schema_version": 1,
  "ruleset_version": 1745040000000,
  "environment": {"key": "production", "protected": false},
  "flags": {
    "checkout": {"type": "boolean", "value": true, "variation": "true", "reason": "targeting_match",
                 "automatic_exposure": false, "override_influenced": false}
  },
  "ignored_attributes": [{"name": "favourite_colour", "code": "undeclared"}]
}
```

The answer carries `Cache-Control: no-store`: one context's values are never cached for another. Evaluating records no exposure; an exposure is the SDK's explicit call. The server counts each evaluation it answers by its true reason, as an [evaluation summary](events.md#evaluations-answered-remotely) of its own.

## What each key learns

A server key's answer carries the true reason and, for a rule, the `rule_id`. A client key's answer says that targeting decided a value but never how: `rule`, `rollout`, `fallthrough` and `prerequisite_failed` are all answered `targeting_match`, with no rule identifier, while `off`, `override`, `experiment`, `error` and the experiment block are answered as they are. See [what a client key can learn](compatibility.md).

## Attributes

Attributes are typed against the project's declarations, as an SDK's own evaluator would type them:

- An attribute the project does not declare, or one named like a unit type (which never stands in for the unit), is left out and listed in `ignored_attributes` with `undeclared`.
- A value of the wrong type is left out and listed with `type_mismatch`. A `string` or `semver` attribute takes a string, a `number` a number, a `boolean` a boolean, a `date` an RFC 3339 string or Unix milliseconds, and a `list` a list of strings.
- `null` is the attribute absent, never a mismatch.

`ignored_attributes` is ordered by name, and empty when nothing was left out.

## Limits

| Limit | Refusal |
|---|---|
| The body is at most 32 KiB after decompression; it may be sent gzip with `Content-Encoding: gzip` | `413 request_too_large` |
| The body is `application/json` (a `charset` parameter is accepted) | `415 unsupported_media_type` |
| The body is one JSON object with a `context`, unit IDs strings and flag keys strings | `400 invalid_request` |
| At most 64 attributes; strings, in an attribute or in a list, at most 256 characters; lists at most 64 items; unit IDs at most 256 bytes; each unit type given once | `422 invalid_context` |

`422 invalid_context` lists every violation at once in `errors`, each with a JSON Pointer into the body (`/context/attributes/country`, `/context/units/user`), `too_long`, `too_many` or `duplicate`, and `params.max` for a limit.

A replica that cannot load the environment's ruleset yet answers `503 ruleset_unavailable` with `Retry-After`; the SDK keeps the values it holds and retries.
