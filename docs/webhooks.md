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
