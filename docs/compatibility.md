---
title: "Compatibility and support"
description: "What 1.0 promises for each surface, how a deprecation is announced on the wire, which versions get fixes, and what is not a contract."
---

# Compatibility and support

What Flagnetic promises from 1.0 on, for each surface a script, an SDK, a pipeline or an operator can depend on; how a removal is announced; and which versions get fixes. The SDKs' half, which server an SDK build speaks to, is on [Versioning and compatibility](sdks/compatibility.md).

## Until 1.0

Flagnetic is in its 0.x line. Until 1.0.0 only the SDK API and the ruleset format are promised to grow only; the management API and the console may change incompatibly in a minor release, each such change marked as breaking in the release notes. See [what 0.x promises](sdks/compatibility.md#what-0x-promises). Everything below is the promise from 1.0.0.

## What 1.0 promises

From 1.0.0 until 2.0.0, every surface below changes only additively: a script, an SDK, an alert or a pipeline written against 1.0 keeps working on every 1.x server.

| Surface | Where it is defined |
|---|---|
| The management API, `/api/v1` | `api/management.yaml` |
| The SDK API, `/sdk/v1` | `api/sdk.yaml`, [Versioning and compatibility](sdks/compatibility.md) |
| The SCIM API, `/scim/v2` | `api/scim.yaml`, [SCIM](scim.md) |
| The ruleset document | [Downloading the ruleset](sdks/ruleset.md) |
| The project document of export and import | [Exporting and importing a project](transfer.md) |
| The export schema of sinks, and their webhook envelope | [Exported events](exported-events.md) |
| Webhook payloads | [Webhooks](webhooks.md) |
| The command line: commands, flags and exit codes | [Operations](operations.md#commands-and-exit-codes) |
| Configuration variables and file keys | [Configuration](configuration.md) |
| Metric names and labels | [Operations](operations.md#metrics) |
| The public API of every SDK package | each package's README |

### What additive means

For every surface, an addition never changes what existed:

- **Documents and payloads** (the APIs, the ruleset, the project document, the export schema, webhook and sink payloads): a field is never renamed, retyped, removed or given another meaning; a new field is optional, and a consumer ignores fields it does not know. A new request field is optional with a default that keeps the old behaviour. A new operation, a new path or a new document `schema_version` served only to a client that asks for it is an addition.
- **Values of a set**: a value is never removed or renamed. A set gains values only where its documentation says it is open (an event type, a problem code, a reason an SDK must treat as unknown); a client that switches over a closed set may rely on it staying closed.
- **Problem codes and status codes**: an operation keeps the codes it answers for the cases it answered before. A request that was accepted stays accepted, and one refused for a reason stays refused for it; a new refusal applies only to what is new.
- **The command line**: a command, a flag and an exit code keep their meaning; new commands, new flags and new exit codes for new failures are additions. Output meant for a person may change; output meant for a script (JSON, the exit code) follows the rules above.
- **Configuration**: a variable and a file key keep their name, type and meaning; a new variable has a default that keeps the old behaviour, so an upgrade needs no configuration change.
- **Metrics**: a metric keeps its name, type and labels; a new metric, and a new label value, are additions. A new label on an existing metric is not: it changes how it aggregates.
- **Behaviour**: a change that makes a correct, documented use of a surface fail is not additive, however small it looks. A bug fix that brings behaviour in line with the documentation is not a change of the contract.

## Deprecation and removal

A thing to remove is deprecated in a minor release and removed only in the next major. Between the two it keeps working as before, and a client can learn of it without reading release notes:

- **An operation of an API** is marked `deprecated: true` in its OpenAPI document, with the date it was deprecated in `x-deprecated-since` and, when one is set, the date it may disappear in `x-sunset`. The server answers every request to it with `Deprecation: @<unix seconds>` ([RFC 9745](https://www.rfc-editor.org/rfc/rfc9745)), the time the deprecation began, and with `Sunset: <HTTP date>` ([RFC 8594](https://www.rfc-editor.org/rfc/rfc8594)) when a sunset is set. The headers are on every answer to the operation, errors included, and nothing else about the answer changes. The API reference marks the operation deprecated.
- **Every SDK** logs a `Deprecation` header it receives once per operation and process, at warning level, with the operation and the sunset when there is one. It never fails a call for it.
- **Anything without an operation** (a field, a command, a flag, a variable, a metric, a payload) is deprecated in the documentation of its surface, where it is marked deprecated with the version that deprecated it and what replaces it, and in the release notes of that version.
- **A sunset date is not a removal date**: a deprecated thing is removed in a major release, never before; the date is the earliest a major release may remove it.

## Support

Only the latest minor release is supported. Fixes, security fixes included, ship in the next patch or minor release from `main`; there are no release branches and no backports. Upgrades between minors are rolling and need no downtime, so moving to the latest minor is cheap; see [Upgrades](upgrades.md). Every SDK is versioned and supported the same way, on its own line.

## What is not a contract

These may change in any release, including a patch:

- The console: its screens, routes, wording and layout. Use the APIs.
- Log lines: their wording, fields and levels. Use metrics and the audit log.
- Internal tables and the database schema: read through the APIs and the sinks, never the tables.
- The `/healthz` and `/readyz` answers' bodies; their status codes are the contract.
- Anything a page marks as experimental, and the output of a command meant for a person.
- Behaviour no page documents.
