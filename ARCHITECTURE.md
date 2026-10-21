# Architecture

This document is the map for contributors: what lives where, which way dependencies point, and why. It describes the target shape; the code is being assembled into it slice by slice, so some directories below do not exist yet.

## The system in one paragraph

Flagnetic is one Go binary with an embedded web console, backed by PostgreSQL and nothing else. SDKs download a **ruleset** (the flags, segments and experiments of one environment) and evaluate it in memory, so a flag check never makes a network call. The server keeps rulesets current over polling and server-sent events, ingests exposures and evaluation summaries from SDKs, and offers a management API that everything in the console is built on.

## Repository layout

```
api/            OpenAPI documents: management, SDK and SCIM APIs. The contracts are written first; code is generated from them.
cmd/flagnetic/  The binary's entry point and composition root.
internal/       The server (layers below).
eval/           The reference evaluator: its own Go module, standard library only, shared by the server and the Go SDK.
conformance/    Language-neutral evaluation fixtures and SDK protocol scenarios every SDK must pass.
sdks/           SDKs, one directory per language, each released on its own tag.
web/            The console (React), built into the binary.
e2e/            End-to-end harness that runs the real binary against each SDK.
docs/           User and operator documentation.
```

## Layers of the server

```
internal/
  domain/<context>/     Entities, value objects, invariants. Plain Go.
  app/<context>/        Use cases: command and query handlers, and the ports they need.
  infra/in/...          Inbound adapters: HTTP APIs, SSO callbacks, workers, CLI.
  infra/out/...         Outbound adapters: PostgreSQL repositories, mail, ClickHouse, object storage, outbound HTTP.
  shared/               Kernel types every layer may use: error kinds, IDs, clock, secret values.
  pkg/                  Code that knows nothing about Flagnetic (for example the transaction-in-context helper).
```

**Dependencies point inwards.** `infra` may import `app` and `domain`; `app` may import `domain`; `domain` imports only `shared`, `eval` and the standard library, and never `pgx`, `net/http` or `database/sql`. `shared` imports only the standard library. `pkg` imports nothing from `internal` but other `pkg` packages, which know nothing about Flagnetic either. These rules are enforced by `depguard` in CI (see `.golangci.yml`), so a violation fails the build rather than a review. The application rule covers handlers and not their tests: an application test wires the real PostgreSQL repositories from `infra`, because repositories are never faked. The domain rule covers tests too.

**Domain.** Rules that must always hold live here and nowhere else: a flag key's pattern, which variation a rule may serve, an experiment's status transitions, which permissions a grant may carry. Domain code does no I/O and reads no clock: time and IDs are passed in. It is tested with plain unit and property tests.

**Application.** One handler per use case, a struct holding exactly the dependencies it uses, rather than a service with twenty methods. Commands change state, queries read it. A handler declares the ports it needs as small interfaces next to itself (the consumer defines the interface), loads aggregates through repositories, calls the domain, and saves. Command and query types are the application's own; generated OpenAPI types never reach this layer.

**Infrastructure.** Inbound adapters translate a request into a command or query and a result back into a response; they hold no rules. Outbound adapters implement ports: one PostgreSQL repository package per context, with hand-written SQL.

Data crosses layers as: HTTP DTO → command → domain model → result → HTTP DTO.

## Bounded contexts

| Context | Owns |
|---|---|
| workspace | projects, environments, unit types, declared and discovered attributes |
| access | members, sessions, tokens, permissions, roles, grants, SSO and SCIM identities |
| flags | flags, variations, configurations per environment, rules, rollouts, overrides, prerequisites, the kill switch, segments and their conditions |
| experiments | experiments, their status machine, assignment, the sample-ratio check |
| delivery | rulesets, their versions and materialization, SDK keys |
| events | exposures, aliases, evaluation summaries, rollups, retention |
| governance | protected environments, change requests, scheduled changes, the audit log, webhooks, notices |
| integrations | sinks to ClickHouse, object storage and webhooks; import and export |

A context reaches another only through an application port, never by importing the other context's adapters.

## Transactions

A command handler is the unit of work. It wraps its work in `Transactor.Within(ctx, fn)`; repositories take the transaction from the context, or use the pool outside one. No port or domain type mentions the database driver. A command that touches several aggregates (applying a change request writes a flag configuration, its audit entry and an outbox row) does it all inside one `Within`. A `Within` inside another joins it, so a handler composed of others is still one unit of work. A query that must see one consistent view across several statements runs in `Transactor.Snapshot` (repeatable read, read only). Row locks are explicit repository methods, so the lock order of a use case is visible in its handler; a deadlock or serialization failure is not retried but answered as `unavailable`, because a retry would hide a lock order that is wrong. Side effects outside the database (webhooks, mail, sinks) go through an outbox written in the same transaction and delivered by workers.

## Errors

Domain and application code return errors of a fixed set of kinds from `internal/shared/errors`, each with a stable snake_case code that clients branch on, a detail for humans, and optionally field violations (a JSON Pointer into the request body, a code and a detail), code-specific extensions and a wrapped cause that is logged and never sent. The kinds and the status every API answers them with:

| Kind | Status |
|---|---|
| `invalid` | 422 |
| `not_found` | 404 |
| `conflict` | 409 |
| `forbidden` | 403 |
| `unauthenticated` | 401 |
| `precondition_failed` | 412 |
| `gone` | 410 |
| `rate_limited` | 429, with `Retry-After` |
| `unavailable` | 503, with `Retry-After` when the wait is known (such as `busy`) |

Only the HTTP adapter answers problems about the HTTP request itself: 400 `invalid_request` (a request it cannot decode), 404 `not_found` and 405 `method_not_allowed` (no route), 408 `request_timeout` (a body that did not arrive in time), 413 `request_too_large` (a body over its limit), 415 `unsupported_media_type` (a body sent as a media type the operation does not accept) and 428 `precondition_required` (a write without `If-Match`). Any error carrying no kind is 500 `internal`, answered with no detail and logged with its cause and the request ID. A request whose client went away before it was answered is the exception: an error that comes from the context net/http served it under being canceled, with no cause of the server's own, is answered nothing and recorded as 499 in the access log and the metrics, logged only at debug, because a browser navigating away is no failure of the server and must not reach the error log or the 5xx count. A server deadline, a handler cancelling a context it derived, and the replica ending its requests when it stops (when the grace period runs out, or at once on a second signal), stay 500. Every API answers errors as RFC 9457 problem documents (`application/problem+json`) written in one place in `internal/infra/in/http`; nothing below the adapter knows about HTTP.

## Identifiers and time

Every identifier is a UUIDv7 minted by the application, never by a column default: an application handler draws it from an `id.Source` (`internal/shared/id`) and hands it to the domain, so a domain object has its identity before it is stored. Identifiers are typed per entity (`type ProjectID = id.Of[Project]`, declared in the entity's domain package), so one entity's identifier passed where another's is expected does not compile; the PostgreSQL adapter converts them to and from `uuid` columns. Being time-ordered, they keep index inserts local and make keyset cursors follow creation order.

Every instant the server reasons about comes from `internal/shared/clock`: handlers read the clock and pass the time to the domain, and to SQL as a query parameter. SQL never reads the database clock (`now()`, `current_timestamp` and their relatives), not even in a column default; a test over the embedded migrations and the SQL of the outbound adapters fails on any of them. A test therefore moves all time at once with the fake clock, and replicas judge time by their own wall clocks, which is why a deployment needs NTP.

## Contracts

The OpenAPI documents in `api/` are the source of truth for the management, SDK and SCIM APIs; servers and the console's client are generated from them, and CI fails if generated code is stale. The SDK protocol and ruleset format are versioned: once an SDK is released, `/sdk/v1` and the ruleset `schema_version` only change additively, because a mobile app in a store keeps calling the server for years.

## The permission seam

Every operation of the management API declares in `api/management.yaml` what it requires of its caller (`x-permission`): a permission of the catalog in `internal/domain/access`, judged in the project the operation addresses, or one of `public`, `member` and `instance_admin`; `x-session-required: true` marks an operation no API token may perform. `task generate` turns the annotations into the operation table of `internal/infra/in/http/management/operations`, and refuses an operation without an annotation or naming a permission the catalog does not hold, so no operation is ever open by accident.

One seam in the HTTP adapter judges every request against that table before the generated handler reads a parameter or a body: it authenticates the bearer token or the session cookie and refuses a cookie-authenticated request from another origin; for an operation in a project, it looks up the addressed project and asks the access context, through an application port, for the caller's effective permissions there; then it authorizes the caller, refusing in this order a project token outside its project, an API token where a session is required, a token lacking the scope, an unknown project and a missing permission. An operation that opens a session also refuses a body that is not JSON. Only then does the handler run. The rules themselves (effective permissions, scope narrowing, what a project token reaches, the grantor cap) are plain domain functions, unit-tested without HTTP. The resolved caller and project travel in the request's context, where handlers read who is acting and audit entries read their actor. Handlers never judge access again, and the console decides nothing either: it reads the caller's permissions and only spares them a refusal.

A permission belongs to the catalog, never to a role check in code: code asks whether the caller holds `environment.manage`, not whether they are a maintainer, so the built-in roles can grow and custom roles can arrive without touching a handler.

## Localization

The console is written in English and translated, through Lingui, with the English text as each message's ID. Every string a member reads is a message: the lint rule `lingui/no-unlocalized-strings` refuses any other, CI fails when extraction would change the committed catalogs or a language lacks a translation, and a pseudo-locale check renders the forms and lists of signing in, accepting an invitation, personal settings, project tokens and auth settings in a locale that marks every message, and fails on text without the mark, which was assembled outside a message. The language is the member's choice when they made one (kept with the member, so it follows them to every browser), else the one this browser remembers, else the first of the browser's languages the console offers, else `FLAGNETIC_DEFAULT_LOCALE`; it is set on `<html lang>`, and every date, number and list is formatted with `Intl` in that language. Role and permission names are messages too, looked up by their identifiers.

The server writes no language. A problem's `detail` is English, for logs and scripts; the console shows a message of its own chosen by the problem's `code`, filled from the problem's extension members and its violations' `params`, and falls back to `detail` only for a code it does not know. A new error code therefore needs its message in the console, not a translated string in Go.

## Evaluation

`eval/` is the reference evaluator. Every SDK implements the same semantics and proves it against `conformance/fixtures` (inputs, rulesets, expected results) and `conformance/protocol` (scripted exchanges with a mock server). A change to evaluation lands as a fixture first; every evaluator stays red until it passes. Bucketing is a published formula (MurmurHash3 over salt and unit), so anyone can recompute a unit's variation.

## Tests

- **Domain**: unit and property tests, no I/O.
- **PostgreSQL adapters and application handlers**: against a real PostgreSQL (testcontainers locally, a service container in CI), one migrated schema per package. Repositories are never faked.
- **HTTP APIs**: black-box acceptance tests through the generated clients against an in-process server.
- **Evaluators**: the conformance fixtures. **SDKs**: a mock server per the protocol scenarios, plus one end-to-end run against the real binary.
- **Console**: Vitest for components, Playwright against a running server.

Tests observe state through the API or a repository's query, never by reading tables. Coverage floors per layer are enforced in CI.

## Background work

Workers (ruleset notifications, rollups, retention, webhook and sink delivery, scheduled changes) run in every replica under one runner that recovers panics and exports metrics. Replicas coordinate through PostgreSQL: advisory locks, `FOR UPDATE SKIP LOCKED` leases, and `LISTEN/NOTIFY`.
