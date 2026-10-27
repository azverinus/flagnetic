# Webhooks

A webhook subscription is a project's standing request to be sent chosen events over signed HTTP (or an instance's, for its own audit entries: see [Instance subscriptions](#instance-subscriptions)), so that chat channels, audit archives and deployment tooling learn of changes without polling. Managing a project's subscriptions, reading them included, needs the `webhook.manage` permission, which maintainers and owners hold (see [Access](access.md)).

## Subscriptions

`POST /api/v1/projects/{project}/webhooks` creates one:

```json
{
  "name": "Security channel",
  "url": "https://hooks.example.com/flagnetic",
  "events": ["flag.kill_switch", "flag.rules_changed"],
  "environments": ["production"]
}
```

- `events` names the types wanted. `*` means every type but `audit.entry`, which copies every audit entry of the project and is asked for by name, so a chat channel subscribed to `*` does not receive each change twice.
- `environments` limits the events of an environment to those named; empty or absent means every environment. An event of no environment is always sent.
- `enabled` is true unless the request says otherwise.

The answer carries `secret`, 32 random bytes in base64url, which signs every call made under the subscription. It is shown this once: the platform stores it sealed under `FLAGNETIC_SECRET_KEY` and never answers it again, and audit entries record it as `[redacted]`. Keep it with the receiver.

`GET …/webhooks` lists a project's subscriptions, the oldest first, and `GET …/webhooks/{webhook}` reads one; neither carries the secret. `PATCH …/webhooks/{webhook}` changes the properties it names, under `If-Match` on the subscription's version; `DELETE …/webhooks/{webhook}` removes it, with no `If-Match`. Each write is audited as `webhook.created`, `webhook.updated` or `webhook.deleted`.

### Rotating the secret

`POST …/webhooks/{webhook}/rotate-secret` issues a new secret and answers it once, as creation does, audited as `webhook.secret_rotated` with both secrets `[redacted]`. The secret it replaces keeps signing beside the new one until `previous_secret_expires_at`, `FLAGNETIC_WEBHOOK_SECRET_OVERLAP` (24 hours) from the rotation: meanwhile each call carries two `v1=` signatures, one per secret, and a receiver holding either accepts it. Roll a receiver over by rotating, giving the receiver the new secret, and letting the overlap run out. Only two secrets sign at once: a rotation within the overlap of another drops the oldest. It takes no `If-Match` and needs `webhook.manage`.

### When a subscription keeps failing

The platform disables a subscription whose receiver is dead: once 25 attempts in a row have failed and none has succeeded for 24 hours, counted from its last success or from when it was last enabled, whichever is later. A short outage of a busy receiver fails 25 attempts in minutes and is retried; a day without one success is what disables. The subscription's `enabled` turns false with `disabled_reason: persistent_failure`, its pending deliveries are `dropped` and never sent, and the change is audited as `webhook.disabled` by the system. The project's other subscriptions naming `webhook.disabled` (or `*`) are sent it, with `audit_entry_id` null and `data` naming the subscription and why its last attempt failed, never its target:

```json
{"subscription_id": "0192…", "name": "Security channel", "last_error": "the receiver answered 503"}
```

A `PATCH` setting `enabled: true` on such a subscription is refused `409 webhook_disabled`. `POST …/webhooks/{webhook}/enable` enables it: `disabled_reason` cleared, `consecutive_failures` reset to 0 and the day counted again from now, audited as `webhook.enabled`. Events are queued for it from then on; the deliveries dropped stay dropped, and a delivery in the log can be redelivered. It enables a subscription a member disabled too, answers an enabled one as it is, takes no `If-Match` and needs `webhook.manage`.

A subscription is refused with 422 and one of these codes:

| Code | Why |
|---|---|
| `invalid_webhook` | a blank name or one over 200 characters, a `url` that is not an absolute http or https URL without credentials or a fragment, no event type, or an environment the project does not have; `errors` points at each |
| `unknown_event_type` | an event type outside the catalogue |
| `insecure_url` | an `http` target, unless the operator set `FLAGNETIC_WEBHOOK_ALLOW_HTTP` |
| `private_network_target` | a target whose name resolves to a loopback, private, link-local, unspecified or multicast address, unless the operator set `FLAGNETIC_WEBHOOK_ALLOW_PRIVATE_NETWORKS` |

A name that does not resolve when the subscription is written is accepted: the receiver may not be up yet.

## Instance subscriptions

An instance admin subscribes an archive to every audit entry that names no project: instance settings, members, sign-ins and project deletions (`project.deleted`, naming the project's id, key and name). `/api/v1/webhooks` and its children work as a project's `/api/v1/projects/{project}/webhooks` do, from creating, editing, enabling and deleting to rotating the secret, testing, reading the delivery log and redelivering, and need `instance_admin`:

```json
{
  "name": "Audit archive",
  "url": "https://archive.example.com/flagnetic",
  "events": ["audit.entry"]
}
```

`events` is exactly `audit.entry`: any other type, `*` included, is refused `unknown_event_type`. An instance has no environments to filter on, so `environments` must be empty or absent, else `invalid_webhook`. Each event's `project` is null, and `data` carries the entry as `GET /api/v1/audit` lists it. A project's entries never reach an instance subscription; a project's own subscriptions receive them. The writes to an instance subscription are audited as entries of the instance, so the subscription receives them too, its own creation first.

## The event catalogue

`GET /api/v1/webhook-event-types` lists every type a subscription may name, with what emits it; any member may read it.

## Deliveries

A change commits together with its events: a write that rolls back notifies nobody. Every few seconds the platform turns the events of each change into one delivery per subscription that wants it: an enabled subscription of the project naming the event's type (or `*`, which matches every type but `audit.entry`), naming the event's environment or none (an event of no environment, such as a flag created, passes every environment filter), and created no later than the change. A subscription created now therefore starts with no backlog.

Every event is sent as one envelope, which every delivery of the event carries unchanged:

```json
{
  "id": "0192f1c4-7a10-7c3e-9a51-3f0c2b6d8e11",
  "type": "flag.kill_switch",
  "timestamp": "2026-03-02T10:00:00Z",
  "project": {"id": "0192…", "key": "shop"},
  "environment": {"id": "0192…", "key": "production"},
  "actor": {"kind": "member", "id": "0192…", "name": "Ada"},
  "entity": {"kind": "flag", "id": "0192…", "key": "checkout"},
  "data": {"reason": "checkout 500s", "before": {"on": true, "…": "…"}, "after": {"on": false, "…": "…"}},
  "audit_entry_id": "0192…"
}
```

`id` identifies the event; a receiver that may see one twice deduplicates on it. `environment` is null for an event of no environment, and `audit_entry_id` names the [audit entry](access.md#the-audit-log) the event comes from. `data` holds the entity's values `before` and `after` the change as the audit entry records them; `flag.rules_changed` adds `diff`, the JSON Patch (RFC 6902) from the rules before to those after, and `flag.kill_switch` adds the `reason`. The `segment.*` events leave out a private segment's conditions; `audit.entry` carries the entry as the audit log reads it, so a subscription naming it receives them as members of the project read them. A kill switch is one `flag.kill_switch` and never a `flag.switched_off` as well, while a write that switches a flag on and changes its rules is both a `flag.switched_on` and a `flag.rules_changed`, sharing their `audit_entry_id`. `audit.entry` carries the entry as `GET /api/v1/projects/{project}/audit/{entry}` reads it, for every change, those with no event of their own included.

A delivery is kept in the log for 30 days after it was made, then pruned; a pending one is kept whatever its age, since it waits to be sent.

`GET …/webhooks/{webhook}/deliveries` lists a subscription's deliveries, newest first, filtered by `status` (`pending`, `succeeded`, `failed`, `exhausted`, `dropped`), `event_type` and the time they were made (`from` and `to`, both included), a page of `limit` at a time with `cursor` set to the previous page's `next_cursor`. `GET …/webhooks/{webhook}/deliveries/{delivery}` reads one with every attempt made. Both need `webhook.manage`.

## Sending

Every `FLAGNETIC_WEBHOOK_TICK` (5 s) each replica sends the deliveries that are due: a `POST` of the envelope to the subscription's `url`, with these headers.

| Header | Value |
|---|---|
| `Content-Type` | `application/json` |
| `X-Flagnetic-Event` | the event's `id`, the same on every delivery of the event; deduplicate on it |
| `X-Flagnetic-Event-Type` | the event's type |
| `X-Flagnetic-Delivery` | the delivery's id, the same on each of its attempts |
| `X-Flagnetic-Attempt` | the attempt's number, from 1 |
| `X-Flagnetic-Timestamp` | when the call was signed, in Unix seconds |
| `X-Flagnetic-Signature` | `v1=` and the lower-case hex of HMAC-SHA256 over `{timestamp}.{body}`, keyed by the subscription's secret as it was shown; while a rotated secret still signs, one such value per secret, comma separated |

A receiver recomputes the signature over the exact bytes it received, compares it in constant time with each value of the header, and refuses a timestamp further than five minutes from its own clock, so a captured call cannot be replayed later:

```go
func verified(r *http.Request, body []byte, secret string) bool {
	ts := r.Header.Get("X-Flagnetic-Timestamp")
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || time.Since(time.Unix(sec, 0)).Abs() > 5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	want := "v1=" + hex.EncodeToString(mac.Sum(nil))
	for _, v := range strings.Split(r.Header.Get("X-Flagnetic-Signature"), ",") {
		if hmac.Equal([]byte(strings.TrimSpace(v)), []byte(want)) {
			return true
		}
	}
	return false
}
```

```python
import hashlib, hmac, time

def verified(headers, body: bytes, secret: str) -> bool:
    ts = headers["X-Flagnetic-Timestamp"]
    if abs(time.time() - int(ts)) > 300:
        return False
    want = "v1=" + hmac.new(secret.encode(), ts.encode() + b"." + body, hashlib.sha256).hexdigest()
    return any(hmac.compare_digest(v.strip(), want) for v in headers["X-Flagnetic-Signature"].split(","))
```

```js
import { createHmac, timingSafeEqual } from "node:crypto";

function verified(headers, body /* Buffer */, secret) {
  const ts = headers["x-flagnetic-timestamp"];
  if (Math.abs(Date.now() / 1000 - Number(ts)) > 300) return false;
  const want = Buffer.from("v1=" + createHmac("sha256", secret).update(`${ts}.`).update(body).digest("hex"));
  return headers["x-flagnetic-signature"].split(",").some((v) => {
    const got = Buffer.from(v.trim());
    return got.length === want.length && timingSafeEqual(got, want);
  });
}
```

An attempt waits 10 seconds for the answer and follows no redirect. A `2xx` answer is a success. No answer (a timeout, a refused or dropped connection), `408`, `429` and `5xx` are retried after 10 s, 1 min, 5 min, 30 min, 2 h, 6 h and 12 h, each moved by up to 20 % either way; after eight attempts the delivery is `exhausted`. Any other status, a `3xx` included, is the receiver refusing the call, and the delivery `failed` at once. The address the target's name resolves to is checked at every connection: one the operator does not allow (see [the codes above](#subscriptions)) is not called, and the attempt is recorded as failed with no answer and retried, since what a name resolves to can change.

Delivery is at least once and in no particular order: a replica stopping in the middle of an attempt leaves the delivery to be attempted again a minute or so later, and a receiver may then see the event twice. Deduplicate on `X-Flagnetic-Event`. At most four deliveries of one subscription are attempted at once, counting every replica's, so a busy project never floods a receiver.

Every attempt shows in the delivery log with its time, status code, latency, the first kibibyte of the answer and, when no answer came, why. A subscription counts its consecutive failed attempts and its last success, which [the disable policy](#when-a-subscription-keeps-failing) reads. A subscription a member disabled is sent nothing: its pending deliveries wait until it is enabled again.

`POST …/webhooks/{webhook}/test` queues a `ping` for the subscription alone, whatever event types it names, and answers its pending delivery (`202`); the ping names the project and the caller, and its environment, entity and audit entry are null. `POST …/webhooks/{webhook}/deliveries/{delivery}/redeliver` queues the delivery's event again as a new delivery, the same event `id` and body byte for byte, and answers it (`202`); the delivery redelivered stays as it was. Both refuse a disabled subscription with `409 webhook_disabled` and need `webhook.manage`.
