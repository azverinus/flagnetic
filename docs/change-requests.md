# Change requests

A change request proposes a change for someone to review before it is made. For now it proposes one [flag](flags.md)'s configuration in one environment: what the configuration would be, and the diff from what it is now. Proposing changes nothing any SDK receives. Approving and applying change requests come later; this page covers writing them.

Every operation below needs the `change_request.propose` permission (token scope `flags:write`, developer and above), reads need `project.read`. A project token proposes like a member of its role: it is the author of its change requests and acts on its own only.

## Protected environments

A change request may propose a change to any environment. In a [protected environment](governance.md#protected-environments) a direct write that would change what SDKs receive is refused `403 change_request_required`, the kill switch excepted: such a change is proposed as a change request instead.

## States

| State | |
|---|---|
| `draft` | Its author prepares it. |
| `open` | Offered for review. |
| `approved`, `rejected` | Decided. |
| `applied` | Made. |
| `cancelled` | Abandoned by its author. |
| `superseded` | What it was based on moved before it was applied. |

`rejected`, `applied`, `cancelled` and `superseded` are final: a copy carries the work on.

## Proposing

`POST /projects/{project}/change-requests` takes:

| Field | |
|---|---|
| `kind` | `flag_configuration`. |
| `flag`, `environment` | The keys of the flag and of the environment. |
| `title` | 1 to 200 characters. |
| `description` | At most 10,000 characters. |
| `proposed` | The whole configuration, as `PUT …/flags/{flag}/environments/{environment}` takes it. |
| `open` | `true` opens it for review at once; it is a draft otherwise. |

`proposed` is checked exactly as writing it would be, with the same refusals, their fields under `/proposed` (`422 invalid_configuration`, `409 archived`). A change request is kept while others write the configuration, so it may name what the configuration no longer holds: a rule `id` the configuration has no rule with, and a rollout `salt` that is not that rule's, are left out, and the rule is a new one. New rules and rollouts are given an identifier and a salt, which `proposed` holds.

The change request answers `proposed` as it would leave the configuration, and `diff`, the [RFC 6902](https://www.rfc-editor.org/rfc/rfc6902) JSON Patch from the configuration it is based on (`base_version`) to `proposed`. A rule inserted or removed is one operation, and a field changed inside a rule is one operation on that field. Who wrote the configuration and when are not part of it.

## Its life

Each of these is a `POST` to `/projects/{project}/change-requests/{id}/…`, or a `PATCH` of the change request, and needs `If-Match` with its version (`428 precondition_required`, `412 version_mismatch`). Only the author takes them (`403 not_author`), and only from the states listed (`409 invalid_state`).

| Operation | From | |
|---|---|---|
| `PATCH` | `draft`, `open` | Changes `title`, `description` or `proposed`; what is absent stays. `diff` is recomputed against the same base. An edit that changes nothing records nothing. |
| `/rebase` | `draft` | Bases the change request on the configuration as it is now: `base_version` moves, `diff` is recomputed, `proposed` stays. |
| `/open` | `draft` | Offers it for review. Refused with `409 base_moved` when the configuration was written since `base_version`, until it is rebased: a reviewer reads the diff against what the environment serves now. |
| `/cancel` | `draft`, `open`, `approved` | Abandons it. |
| `/copy` | any | Copies any change request, whoever wrote it, into a new draft of the caller's with its title, description and proposal, based on the configuration as it is now and checked again. Needs no `If-Match`. |

Every transition is recorded in the audit log as `change_request.drafted`, `opened`, `updated`, `rebased`, `cancelled` or `copied`.

## Reading

`GET /projects/{project}/change-requests` lists them newest first, filtered by `flag` and `environment` (keys), `kind`, `state` and `author` (a member's or a project token's identifier), a page of `limit` (at most 200, 50 by default) at a time with `cursor`. `GET …/change-requests/{id}` reads one, its version as its `ETag`.
