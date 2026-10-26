# Change requests

A change request proposes a change for someone to review before it is made: one [flag](flags.md)'s configuration in one environment, or a flag's or a [segment](segments.md)'s definition in every environment; what it would be, and the diff from what it is now. Proposing changes nothing any SDK receives; a member who is not the author approves it, and applying it makes the change.

Proposing and applying need the `change_request.propose` permission (token scope `flags:write`, developer and above); approving and rejecting need `change_request.approve` (maintainer and above, members only); reads need `project.read`. A project token proposes like a member of its role: it is the author of its change requests and acts on its own only, and it never decides one.

## Protected environments

A change request may propose a change to any environment. In a [protected environment](governance.md#protected-environments) a direct write that would change what SDKs receive is refused `403 change_request_required`, the kill switch excepted: such a change is proposed as a change request instead.

## States

| State | |
|---|---|
| `draft` | Its author prepares it. |
| `open` | Offered for review. |
| `approved`, `rejected` | Decided. |
| `applied` | Made. |
| `cancelled` | Abandoned by its author, or by a member who may decide change requests. |
| `superseded` | What it was based on moved before it was applied. |

`rejected`, `applied`, `cancelled` and `superseded` are final: a copy carries the work on.

## Proposing

`POST /projects/{project}/change-requests` takes:

| Field | |
|---|---|
| `kind` | `flag_configuration` or `definition`. |
| `flag`, `environment`, `segment` | The keys of what it proposes for: a configuration names a `flag` and an `environment`; a definition names one `flag` or one `segment`, and no environment (`422 invalid_change_request` otherwise). |
| `title` | 1 to 200 characters. |
| `description` | At most 10,000 characters. |
| `proposed` | What the change request proposes, by its kind and entity (below). |
| `revert_to` | In place of `proposed`, for a flag's configuration: the version to revert it to, from which the server fills `proposed` (see [Reverting a protected environment](governance.md#reverting-a-protected-environment)). |
| `open` | `true` opens it for review at once; it is a draft otherwise. |

| Proposes | `proposed` |
|---|---|
| A flag's configuration | The whole configuration, as `PUT …/flags/{flag}/environments/{environment}` takes it. |
| A flag's definition | `automatic_exposure` and every one of its `variations`, in order, as `PATCH …/flags/{flag}` takes them. |
| A segment's definition | Its `conditions` and `private`. |

A definition is what every environment's ruleset carries of the flag or the segment, so one change request covers every environment. A flag's name, description, owner, tags and lifetime, a variation's name and description when nothing else changes, and a segment's name and description change nothing SDKs receive: they are edited directly, in a protected environment too.

`proposed` is checked exactly as writing it would be, with the same refusals, their fields under `/proposed` (`422 invalid_configuration`, `422 invalid_flag`, `422 invalid_segment`, `409 archived`, `409 in_use` for a variation something still serves). A change request is kept while others write the configuration, so it may name what the configuration no longer holds: a rule `id` the configuration has no rule with, and a rollout `salt` that is not that rule's, are left out, and the rule is a new one. New rules and rollouts are given an identifier and a salt, which `proposed` holds.

The change request answers `proposed` as it would leave the configuration, the flag or the segment, and `diff`, the [RFC 6902](https://www.rfc-editor.org/rfc/rfc6902) JSON Patch from what it is based on (`base_version`, the version of the configuration, the flag or the segment) to `proposed`. A rule inserted or removed is one operation, and a field changed inside a rule is one operation on that field. Who wrote the configuration and when are not part of it.

## Its life

Each of these is a `POST` to `/projects/{project}/change-requests/{id}/…`, or a `PATCH` of the change request, and needs `If-Match` with its version (`428 precondition_required`, `412 version_mismatch`). Only the author takes them (`403 not_author`), but a member holding `change_request.approve` also cancels another's, and only from the states listed (`409 invalid_state`).

| Operation | From | |
|---|---|---|
| `PATCH` | `draft`, `open` | Changes `title`, `description`, `proposed` or `apply_at` (null removes it, see [Scheduled changes](#scheduled-changes)); what is absent stays. `diff` is recomputed against the same base. An edit that changes nothing records nothing. |
| `/rebase` | `draft` | Bases the change request on the configuration, flag or segment as it is now: `base_version` moves, `diff` is recomputed, `proposed` stays; a revert's is filled again from `revert_to`. |
| `/open` | `draft` | Offers it for review. Refused with `409 base_moved` when what it is based on was written since `base_version`, until it is rebased: a reviewer reads the diff against what is served now. |
| `/cancel` | `draft`, `open`, `approved` | Abandons it. |
| `/copy` | any | Copies any change request, whoever wrote it, into a new draft of the caller's with its title, description and proposal, based on its configuration, flag or segment as it is now and checked again; it is not scheduled. Needs no `If-Match`. |

## Approving and applying

| Operation | From | |
|---|---|---|
| `/approve` | `open` | Approves it, with an optional `comment` of at most 2,000 characters. |
| `/reject` | `open` | Rejects it, with a `comment` telling the author why, required and at most 2,000 characters (`422 invalid_change_request` at `/comment`). |
| `/apply` | `approved` | Writes `proposed`. Refused `409 scheduled` when the change request has an apply time. |

Approval is always a second person: a member holding `change_request.approve` approves or rejects, and is refused their own change request (`403 author_cannot_approve`). A project token never approves or rejects (`403 token_cannot_approve`), so a pipeline proposes and a member decides; a member may approve a change request a token they created proposed. One approval is enough. Once approved, a change request is no longer edited: what was approved is what is applied. `approval` and `rejection` answer who decided (`member` with `id` and `name`), when (`at`) and the `comment`.

The author, whoever approved it, or any member holding `change_request.approve` applies an approved change request (`403 not_author` for anyone else, project tokens included). Applying writes the configuration as one write, the same refusals as replacing it, in a [protected environment](governance.md#protected-environments) too: the approval covers the change request's environment. A definition is written as one edit of the flag or the segment, every environment's ruleset moving once; its approval covers the environments protected when it was given. When an environment was protected since, applying it would change that environment without anyone having approved it: the application is refused `409 base_moved` and the change request superseded. An environment no longer protected needs no cover. The rules keep the identifiers and the rollouts the salts `proposed` shows, so the units the reviewers saw fall in a rollout are the ones that do, and the member applying becomes the author of the overrides it changes, as with a direct write. `applied_at` and `applied_by` (`kind` `member`, `token` or `system`, with `id` and `name`) record who applied it.

When what it is based on moved without a write superseding the change request, as restoring a database backup moves it, approving or applying it is refused `409 base_moved` and the change request becomes `superseded`, with no `superseded_by`: what would be approved or written is no longer what was reviewed. Copy it to propose it again on the current base. When `proposed` no longer validates, because a flag or segment it names was archived or deleted since, applying is refused with the same `422` a direct write gets, its fields under `/proposed`; nothing is written and the change request stays approved.

## Scheduled changes

A change request created or edited with `apply_at` is applied by the system at that time once it is approved, instead of by a member. The time is at least `FLAGNETIC_SCHEDULE_MIN_LEAD` (60 seconds by default) and at most 365 days after the create or edit that sets it (`422 invalid_change_request`, `too_soon` or `too_late` at `/apply_at`), and is answered in UTC. It is set on a draft or an open change request only: approval asks no time and does not judge the lead again, and an approved change request's time cannot change. A scheduled change request is never applied by hand (`409 scheduled`): to apply it now, cancel it and copy it, which drops the time.

Every replica looks for approved change requests whose time has come every `FLAGNETIC_SCHEDULE_TICK` (10 seconds by default). One whose time passed while no replica ran is applied as soon as one does, late rather than skipped, and however many replicas find it due it is applied once. The application is the same as by hand, with `applied_by` of `kind` `system` and audit entries made by the system. A change request cancelled or superseded before its time is not applied; one whose base moved without a write superseding it is superseded, as a manual application would be.

When the application fails, as when a flag `proposed` names was archived since the approval, nothing is written, the change request stays approved, and `schedule_failure` records the last failure: `at`, the `code` and `message` of the refusal (`internal` for an error of the server, whose detail is in its log), the `attempts` that failed in a row, and whether the scheduler `stopped`. Recording it changes neither the change request's state nor its version. The scheduler tries again every round until `FLAGNETIC_SCHEDULE_MAX_FAILURES` attempts (5 by default) have failed, then stops and records one `change_request.schedule_failed` entry. Fix what failed and the next round applies it, or, once stopped, cancel it and copy it. The failure is cleared when the change request is applied or cancelled, and kept when it is superseded.

## Superseded

An open or approved change request is superseded the moment any other write moves what it was based on, so applying it never writes over someone's later change:

- a write to its configuration: a direct write in an environment that is not protected, an override set or removed, the kill switch, a rollout's salt regenerated, a copy from another environment, a revert, a stop of an experiment rewriting its rules, or another change request's application;
- archiving or deleting its flag;
- deleting its environment.

A definition was reviewed with the configurations it changes what they serve, so it is superseded by:

- any write to its flag or segment, a rename or a description included, and deleting its segment;
- for a flag's definition, a write to the flag's configuration in any environment;
- for a segment's definition, a write to the configuration, in any environment, of a flag that named the segment when the change request was based on it or names it now, so a write that starts or stops naming it counts.

Applying a definition supersedes no configuration's change request, since no configuration moved.

The write and the supersede commit together. `superseded_by` names the audit entry of the write, and each change request it supersedes gets a `change_request.superseded` entry of its own, made by the writer in the same request, naming that entry in its `superseded_by`. A change request is never superseded by its own application, and a draft is never superseded: it opens only on the current configuration, after a rebase. Deleting an environment or a segment records the change requests it ends before they go with it, so their entries remain in the audit log.

Every read and write of a configuration (`GET`, `PUT`, overrides, salts, the kill switch, revert and copy) answers `change_requests`, the change requests waiting on it: how many are `open`, how many `approved`, and `next_apply_at`, the earliest time one of them is scheduled to be applied, null when none is. A member about to write it sees what the write will supersede. A flag's detail does not count them.

## The audit log

Every transition is recorded in the audit log as `change_request.drafted`, `opened`, `updated`, `rebased`, `cancelled`, `copied`, `approved`, `rejected`, `applied` or `superseded`, and a scheduler that stops trying a change request as `change_request.schedule_failed`. The entries an application writes, the configuration's, the flag's or the segment's, and `change_request.applied`, carry the change request's identifier as `change_request_id`, and the audit log's `change_request` filter lists them, so a change to production traces back to its approval.

## Reading

`GET /projects/{project}/change-requests` lists them newest first, filtered by `flag`, `environment` and `segment` (keys), `kind`, `state`, `author` (a member's or a project token's identifier) and `scheduled` (`true` for those with an apply time, `false` for those without), and with `awaiting_my_decision=true` to the open change requests of others the caller may approve or reject (none for a caller without `change_request.approve`, a project token included), a page of `limit` (at most 200, 50 by default) at a time with `cursor`. `GET …/change-requests/{id}` reads one, its version as its `ETag`.

## In the console

The project's **Change requests** screen lists them in three tabs: those awaiting your decision, your own, and all, with the filters above. The navigation counts the change requests awaiting your decision when you hold `change_request.approve`.

A change request's page shows its flag and environment, its author, the decisions with their comments, and what it would change: compared with the configuration it was based on while it still waits on that version, and otherwise as its patch, operation by operation. Its actions are those its state allows: editing the title and description, opening, rebasing, approving, rejecting, applying, cancelling and copying. What only its author may do is shown to the author alone; an action needing a permission you lack is shown disabled with the permission and the roles holding it. A decision on a change request someone changed meanwhile is refused, and the page shows the newer version.

A flag's environment tab lists the open and approved change requests waiting on that configuration.
