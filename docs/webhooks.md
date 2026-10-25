# Webhooks

A webhook subscription is a project's standing request to be sent chosen events over signed HTTP, so that chat channels, audit archives and deployment tooling learn of changes without polling. Managing a project's subscriptions, reading them included, needs the `webhook.manage` permission, which maintainers and owners hold (see [Access](access.md)).

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

A subscription is refused with 422 and one of these codes:

| Code | Why |
|---|---|
| `invalid_webhook` | a blank name or one over 200 characters, a `url` that is not an absolute http or https URL without credentials or a fragment, no event type, or an environment the project does not have; `errors` points at each |
| `unknown_event_type` | an event type outside the catalogue |
| `insecure_url` | an `http` target, unless the operator set `FLAGNETIC_WEBHOOK_ALLOW_HTTP` |
| `private_network_target` | a target whose name resolves to a loopback, private, link-local, unspecified or multicast address, unless the operator set `FLAGNETIC_WEBHOOK_ALLOW_PRIVATE_NETWORKS` |

A name that does not resolve when the subscription is written is accepted: the receiver may not be up yet.

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

`GET …/webhooks/{webhook}/deliveries` lists a subscription's deliveries, newest first, filtered by `status` (`pending`, `succeeded`, `failed`, `exhausted`, `dropped`), `event_type` and the time they were made (`from` and `to`, both included), a page of `limit` at a time with `cursor` set to the previous page's `next_cursor`. `GET …/webhooks/{webhook}/deliveries/{delivery}` reads one with every attempt made. Both need `webhook.manage`.
