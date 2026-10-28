# Governance

Governance is how a team controls changes to what its SDKs receive: which environments need a second pair of eyes, and how a change reaches them. This page covers protected environments and how a change reaches one; [change requests](change-requests.md) covers each operation in full.

## Protected environments

An environment is protected when what its SDKs receive should change only through a reviewed change request. An owner protects an environment or stops protecting it with `PUT /projects/{project}/environments/{environment}/protection` and `{"protected": true}` or `false`, under `If-Match` on the environment's version; it needs `environment.protect`, which only owners hold and no project token exercises, so a pipeline cannot unprotect an environment, write it and protect it again. Asking for the protection an environment already has changes nothing. Each change is recorded in the audit log as `environment.protected` or `environment.unprotected`, and `protected` is part of every environment the API answers.

The production environment of a project created from this release on starts protected; the other environments, and every environment that existed before the upgrade, start unprotected, so an upgrade does not stop a script writing production directly. Protect production yourself where you want its changes reviewed.

SDKs read the protection from their ruleset and evaluate answers (`environment.protected`), and a server SDK ignores local overrides in a protected environment. Protecting or unprotecting an environment moves its ruleset version, so SDKs learn it at once.

In the console, an environment's page has a **Protected** switch, which asks for confirmation naming what changes; members without `environment.protect` see it disabled with what it takes. A protected environment is marked wherever the console shows it, and a list to choose an environment from names it "Production (protected)".

### What protection refuses

A write that would change what a protected environment's SDKs receive is refused `403 change_request_required`, whatever it touches and whichever endpoint it goes through: a flag's configuration there (replacing it, an override, a rollout's salt, a revert, a copy into it), a flag's variations or automatic exposure, a segment's conditions or private switch when a flag uses the segment there, archiving a flag switched on there or restoring one that was, and an experiment's start, stop or traffic allocation growth when the environment holds a rule serving it. The refusal's `detail` names the protected environments the write would change. Nothing of the write is kept: a write that would change a protected environment and others too, such as editing a segment that staging and production both use, changes none of them.

Protection judges the result, not the endpoint. When a write is about to commit, the server builds each protected environment's ruleset as the write would leave it and compares it with the ruleset that environment serves now. It leaves out what no evaluation depends on, so these writes stay direct in a protected environment:

- a flag's name, description, tags, owner and lifetime, and creating a flag;
- archiving or restoring a flag that is off in the environment;
- creating a segment, and editing or deleting one no flag in the environment uses;
- declaring, editing and removing unit types and attributes;
- an environment's name and order, and its protection itself;
- any write to an environment that is not protected.

A segment counts only when a flag's rule uses it both before and after the write; a rule that starts or stops using a segment is a change of that flag. A flag that is off in the environment still counts in full: its rules and its variations' values are what switching it on would serve.

A request is checked in order: its permission first, then whether it is valid, then `If-Match`, and protection last. A write that is malformed or based on an old version says so before it is told to become a change request.

### The kill switch

The kill switch is the one direct write to a protected environment: `POST /flags/{flag}/environments/{environment}/kill` switches the flag off there with the permission it always needs (`flag_configuration.kill`, developer and above), so whoever is on call stops an incident without waiting for an approval. It is audited as every kill switch is, marked `kill_switch`. Switching the flag back on is an ordinary configuration write, which a protected environment takes only through a change request. The console asks for the flag's key to be typed out before a kill switch in a protected environment, so production is not switched off by accident.

### What protection costs

A write that changes no ruleset pays nothing. A write that changes a ruleset pays, for each protected environment it reaches, one build of that environment's ruleset inside its transaction, compared with the ruleset the replica already holds in memory. When the replica does not hold it yet, it reads it on two PostgreSQL connections of its own, outside its pool, so writes waiting for each other never wait for a connection too. `flagnetic_protection_guard_seconds` measures it per write (see [Operations](operations.md#metrics)).

## Changing a protected environment

A change to a protected environment goes through a [change request](change-requests.md):

1. A developer proposes the configuration as a change request (`change_request.propose`) and opens it for review; a flag's variations and automatic exposure, or a segment's conditions and private switch, are proposed as one change request covering every environment, and an experiment's start, stop or traffic allocation growth as one covering every environment holding its rule. The change request shows the diff from what the environment serves now.
2. A maintainer or owner who is not the author approves it (`change_request.approve`), or rejects it with a comment. Approval always takes a second person: the author is refused (`author_cannot_approve`).
3. The author, the approver or any member holding `change_request.approve` applies it. The application writes the configuration into the protected environment, past protection for that environment alone, and its audit entries carry the change request's identifier.

A change request may instead name an apply time, `apply_at`, when it is proposed: once approved, the system applies it then, late rather than skipped when no replica was running, and once however many replicas run. A scheduled application that fails records why on the change request and is tried again, until the scheduler stops after five failures in a row. See [Scheduled changes](change-requests.md#scheduled-changes).

A change request is based on the configuration, flag, segment or experiment as it was proposed. If anything writes it before the change request is applied, or archives or deletes its flag, deletes its segment, experiment or environment, it is [superseded](change-requests.md#superseded) by that write at once, since what was reviewed is no longer what would be written; copy it to propose it again. A change request may be proposed for any environment, protected or not.

## Reverting a protected environment

A [revert](flags.md#revert) of a protected environment is refused `403 change_request_required` like any other write: an earlier configuration reaches production only after a review. It is proposed as a configuration change request that names the version instead of a proposal, `revert_to`:

```json
{"kind": "flag_configuration", "flag": "checkout", "environment": "production",
 "title": "Roll the checkout back", "revert_to": 4, "open": true}
```

- The server fills `proposed` with the configuration as that version was, whole: rule identifiers and salts, override notes and authors, so the same units fall in every rollout again. An ordinary change request leaves out identifiers and salts the configuration no longer holds; a revert keeps them.
- It is refused as a direct revert is: `404 version_not_found` for a version not in the history of this flag in this environment, `409 experiment_rules_changed` when it would change which rules serve an experiment, `422 invalid_configuration` under `/proposed` when a variation, segment, attribute or flag it names is gone. Giving both `revert_to` and `proposed`, or `revert_to` on a definition, is `422 invalid_change_request`.
- Nobody edits what a revert proposes (`422 invalid_change_request`, `not_allowed` under `/proposed`), so whoever may propose a revert cannot slip another change in under its name; its title, description and apply time are edited as usual.
- A rebase or a copy keeps `revert_to` and fills `proposed` from it again against the configuration as it is now, with the same refusals.
- Applying it reverts the configuration as a direct revert does, recorded as `flag.reverted` with the version returned to and the change request's identifier.

The change request answers `revert_to`, null for any other change request.

## Pipelines and change requests

A project token proposes, edits, opens, cancels and applies its own change requests like a member of its role, so a pipeline holding a token with `flags:write` can propose a production change and apply it once approved. It never approves or rejects one, whatever its role (`403 token_cannot_approve`), and never cancels or applies another author's: deciding is a member's. A typical pipeline:

1. `POST /projects/{project}/change-requests` with `"open": true` and the configuration it wants, keeping the change request's `id` and `ETag`.
2. Wait until a member approves it: poll `GET …/change-requests/{id}` until `state` is `approved` (or `rejected`, `superseded`, `cancelled`, which end the run).
3. `POST …/change-requests/{id}/apply` with `If-Match` on the version it read.
