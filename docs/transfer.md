---
title: "Exporting and importing a project"
description: "The project document field by field and its compatibility promise, create, merge and replace, dry runs and If-Match, protection and owners, the audit trail, the console, the CLI, importing from Firebase Remote Config, and the demo project."
---

# Exporting and importing a project

A project's configuration can leave the platform as one file and come back. This page documents that file, the **project document**, field by field; the three ways to **import** one (create a project, merge, replace); the **dry run** that shows what an import would do; what protection, owners and the audit trail do with an import; the CLI; and the translation of a Firebase Remote Config template into flags.

Use it to keep a project's configuration in git, copy a project to another instance, start a new instance from a known state, show an auditor the whole configuration, or move from Firebase Remote Config. To try it, import the [demo project](#the-demo-project).

## Contents

- [The project document](#the-project-document)
- [What never travels](#what-never-travels)
- [The compatibility promise](#the-compatibility-promise)
- [Exporting](#exporting)
- [Importing](#importing)
- [Dry runs and `If-Match`](#dry-runs-and-if-match)
- [Protection, owners and permissions](#protection-owners-and-permissions)
- [The audit trail](#the-audit-trail)
- [Limits and refusals](#limits-and-refusals)
- [In the console](#in-the-console)
- [The CLI](#the-cli)
- [Importing from Firebase Remote Config](#importing-from-firebase-remote-config)
- [The demo project](#the-demo-project)

## The project document

A project document is JSON or YAML with these top-level fields, all of which an export writes:

| Field | Holds |
|---|---|
| `schema_version` | `1`. The version of the document format, not of the server. |
| `project` | `{key, name, description}`. |
| `environments` | The project's environments; their order is their sort order. |
| `unit_types` | Declared unit types. |
| `attributes` | Declared attributes, never the standard ones. |
| `segments` | Segments with their conditions. |
| `flags` | Flags with their variations and their configuration in every environment. |
| `experiments` | Experiments in whatever status they are. |

Identity is the key: no entity identifier travels, so a document means the same on any instance. Unknown fields are refused, with a JSON Pointer to each.

### `project`

| Field | Meaning |
|---|---|
| `key` | The project's key. On a create it is the new project's key unless `key` is given as a parameter. A merge or replace never changes the key. |
| `name`, `description` | Written into the project by a merge or replace when `project` is present. |

### `environments[]`

| Field | Meaning |
|---|---|
| `key`, `name` | The environment's key and name. |
| `protected` | Whether the environment is [protected](governance.md). Taken by an import only for an environment the project does not have yet; see [Protection](#protection-owners-and-permissions). |

### `unit_types[]`

`{key, name, description}`: a declared kind of unit, such as `user` or `device`.

### `attributes[]`

`{key, type, name, description}`. `type` is `string`, `number`, `boolean`, `semver`, `date` or `list`; see [Targeting](targeting.md). The attributes every project has are never listed, and a document declaring one under another type is refused.

### `segments[]`

`{key, name, description, private, conditions}`. A segment holds `attribute` conditions only; see [Segments](segments.md).

### Conditions

A condition is written as the management API writes it:

```yaml
- kind: attribute        # attribute | in_segment | not_in_segment | prerequisite
  attribute: plan        # attribute conditions
  op: in
  value: [pro, enterprise]
```

`in_segment` and `not_in_segment` carry `segment`; `prerequisite` carries `flag` and `variation`. The operators are in [Targeting](targeting.md#operators).

### `flags[]`

| Field | Meaning |
|---|---|
| `key`, `type`, `name`, `description` | `type` is `boolean`, `string`, `number` or `json`, and never changes once the flag exists (`type_immutable`). |
| `owner` | The owner's email address. Optional; an export writes `""` for an owner who holds no grant in the project (an instance admin without one, a former member). See [Owners](#protection-owners-and-permissions). |
| `tags` | A list of strings. |
| `lifetime`, `expected_removal_at` | `permanent` or `temporary`; a temporary flag's expected removal instant (RFC 3339), `null` otherwise. |
| `automatic_exposure` | Whether the SDK records an exposure on evaluation. |
| `archived` | An archived flag. |
| `variations[]` | `{key, value, name, description}`. `value` is JSON of the flag's type. A boolean flag has the two variations `true` and `false`. |
| `configurations` | A map from environment key to the flag's configuration there. |

A configuration is:

| Field | Meaning |
|---|---|
| `on` | Whether the flag is on. Quote it in YAML: `"on": true`. |
| `off_variation` | The variation served while it is off. |
| `fallthrough` | The variation served when no rule matches. |
| `prerequisites[]` | `{flag, variation}`: another flag that must serve a variation. |
| `overrides[]` | `{unit_type, unit_id, variation, note}`: a variation fixed for one unit. The override's author does not travel. |
| `rules[]` | Ordered `{id, description, conditions, serve}`. |

`serve` is `{kind: variation, variation}`, `{kind: rollout, variation, unit_type, percentage, salt}` or `{kind: experiment, experiment}`. `percentage` is in basis points (`2500` is a quarter of the units). A rule's `id` and a rollout's `salt` are what keep a unit in its bucket: they travel, so a unit that was in a rollout stays in it in the copy.

### `experiments[]`

| Field | Meaning |
|---|---|
| `key`, `name`, `hypothesis`, `owner`, `tags` | As for a flag. |
| `flag`, `unit_type` | The flag compared and the unit type it is split by. |
| `variations[]` | `{variation, weight}`: each variation's share of the allocated traffic, in basis points. The order is kept. |
| `traffic_allocation` | Basis points of the units that take part. |
| `inclusion_salt`, `variation_salt` | The salts of the assignment; they travel, so assignments survive a copy. |
| `status` | `draft`, `running`, `stopped` or `archived`. |
| `expected_end_at` | A day (`2026-11-05`); an import also reads an RFC 3339 instant and takes its UTC day. |
| `started_at`, `stopped_at`, `archived_at` | RFC 3339 instants, `null` while they do not apply. |
| `decision` | `{outcome, winner_variation, note}` once stopped, otherwise `null`. |

### An example

```yaml
schema_version: 1
project:
  key: shop
  name: Shop
  description: ""
environments:
  - {key: development, name: Development, protected: false}
  - {key: production, name: Production, protected: true}
unit_types:
  - {key: user, name: User, description: ""}
attributes:
  - {key: plan, type: string, name: Plan, description: ""}
segments:
  - key: paying_customers
    name: Paying customers
    description: ""
    private: false
    conditions:
      - {kind: attribute, attribute: plan, op: in, value: [pro, enterprise]}
flags:
  - key: new_checkout
    type: boolean
    name: New checkout
    description: ""
    owner: ada@example.com
    tags: [checkout]
    lifetime: temporary
    expected_removal_at: 2027-03-01T00:00:00Z
    automatic_exposure: true
    archived: false
    variations:
      - {key: "true", value: true, name: "", description: ""}
      - {key: "false", value: false, name: "", description: ""}
    configurations:
      development:
        "on": true
        off_variation: "false"
        fallthrough: "true"
        prerequisites: []
        overrides: []
        rules: []
      production:
        "on": true
        off_variation: "false"
        fallthrough: "false"
        prerequisites: []
        overrides:
          - {unit_type: user, unit_id: u-1001, variation: "true", note: Support case}
        rules:
          - id: 3f0d4c9e-2c2b-4b5e-9d57-6c1f1d6f3a10
            description: Paying customers
            conditions:
              - {kind: in_segment, segment: paying_customers}
            serve: {kind: variation, variation: "true"}
          - id: 8a7c2d51-0b9e-4e4f-a0a1-5e0f4c7b9d22
            description: A quarter of everyone else
            conditions: []
            serve: {kind: rollout, variation: "true", unit_type: user, percentage: 2500, salt: k3p9x0w2m7q1c5ab}
experiments: []
```

### Optional fields on import

A hand-written document may leave out: a rule's `id` and a rollout's `salt` (minted; a present id must be a UUID and a present salt 16 characters of `[a-z0-9]`), a draft experiment's salts (drawn; a present one must be 16 lower-case hexadecimal characters), and `owner`, every `description`, `tags`, `prerequisites`, `overrides` and `note`. In a merge every top-level section is optional (an absent section is left untouched); `create` and `replace` need every section. An export writes every optional field: an absent text is `""`, an absent list `[]`, an absent instant, day or decision `null`.

### Ordering and byte stability

Two exports of an unchanged project are the same bytes, so a diff shows only real changes. Environments are in sort order; every other list and map is by key in byte order, overrides by unit type then unit ID, prerequisites by flag. A list whose order means something keeps it: a configuration's rules, a flag's variations, an experiment's weights, tags. JSON is indented by two spaces with a trailing newline. YAML is the same document field for field, with YAML 1.1 booleans (`"on"`) quoted.

## What never travels

Members and their grants, roles, tokens and SDK keys, webhook subscriptions and sinks, change requests, scheduled changes, the audit log, discovered attributes, events (exposures, aliases, summaries) and the authors of overrides. A document carries no credentials and no personal history; owners are exported as email addresses, only for owners who hold a grant in the project (the members `GET /projects/{project}/members` already lists to the same readers), and nothing else about a person.

## The compatibility promise

The document has its own `schema_version`, apart from the management API's version. A server imports **every** `schema_version` it ever exported and exports the newest, so a file kept in git outlives the server that wrote it. Within a version the format changes only additively: a field may be added, never renamed, removed or given another meaning. A document with another version is refused `422 schema_version_unsupported`, naming the versions the server reads in `supported_versions`.

The OpenAPI document (`api/management.yaml`) holds the document's own schemas, `ProjectDocument` and its parts, independent of the management API's schemas of the same entities.

## Exporting

Anyone who can read a project exports it:

```
GET /api/v1/projects/{project}/export?format=json|yaml
```

The answer is `application/json` or `application/yaml` with a `Content-Disposition` naming the file and an `ETag`: the identifier of the project's latest audit entry, quoted (a project with no audit entry has none), which an import can send back as [`If-Match`](#dry-runs-and-if-match). An unknown `format` is `400`.

## Importing

An import applies whole or not at all: a failure leaves the project as it was. It is one request, `application/json` or `application/yaml` by `Content-Type`.

| Operation | Who | Does |
|---|---|---|
| `POST /projects/import?key=&name=&dry_run=` | instance admin | **create**: a new project holding exactly the document plus the standard attributes. No default environments or unit types are added, and the document needs at least one environment. `key` and `name` override the document's. A taken key is `409 already_exists`. |
| `POST /projects/{project}/import?mode=merge` | project owner | **merge**: creates or updates, by key, every entity the document names, and leaves the rest alone. |
| `POST /projects/{project}/import?mode=replace` | project owner | **replace**: a merge, then deletes what the document does not name. The project's configuration equals the document. |

### Merge

- Entities are matched by key. The project's key never changes; its name and description are written when `project` is present.
- A configuration for an environment the document does not configure is left as it is. A new environment gives every flag its initial configuration there unless the document configures it.
- An existing environment keeps its place in the sort order; new ones are appended after it.
- A rule with no `id` that equals a rule of the project's configuration (description, conditions, serve) is that rule, and a rollout without a `salt` keeps its rule's, so a hand-written document merges as no change the second time.
- A flag's or attribute's type cannot change: `type_immutable`.
- A flag the document archives or restores differently from the project is archived or restored by the ordinary actions.

### Replace

Replace needs every section, as create does, and a configuration only for an environment of the document. After the merge it deletes, in this order: environments, experiments, flags, segments, attributes, unit types. Each deletion is the ordinary deletion and is recorded as such; an absent flag that is not archived is archived first.

A replace is refused whole, and nothing is deleted, when:

- an absent experiment is not a draft: `409 not_draft`, with a violation per experiment;
- anything to delete is still used: `409 in_use` with every reference in `references[{kind, key, environment}]`: a segment a rule names, a flag an experiment of the document holds, a unit type or attribute a rule names;
- an absent environment is protected: `409 environment_protected`;
- an absent environment has live SDK keys (neither revoked nor past a rotation overlap): `409 in_use`, the keys as `sdk_key` references;
- it would leave no environment: this one is `422 invalid_document`, `invalid_value` at `/environments`.

### Experiments

Experiments are written after the flags and before the configurations. A new experiment is created exactly as the document has it: its status, salts, timestamps and decision, which must agree with the status (a running one has `started_at` and `expected_end_at`; a stopped one also `stopped_at` and a decision; an archived one `archived_at`). An import never starts, stops or rewinds an experiment. An existing experiment changes only as an ordinary edit allows in its status; any other difference is `409 frozen`, with a violation naming each field, and a smaller traffic allocation on a running experiment is `409 traffic_allocation_decrease`. A draft the document moves to another flag is `409 in_use`, naming in `references` each flag and environment whose configuration still serves it once the document is written; a document that moves the draft and drops those rules is applied. A replace deletes an absent draft together with the absent flags whose rules serve it; one that a flag of the document still serves is `409 in_use`.

### Salts and rule ids

Rule ids and salts in the document are kept, and missing ones are minted. A rollout salt may repeat only as the same rule of one flag in other environments (what copying a configuration keeps); any other repeat is refused as `duplicate`. No salt is shared by two experiments or by an experiment and a rollout.

### An import that changes nothing

An entity equal to the document is not written. An import that changes nothing writes no audit entry, bumps no ruleset and answers empty changes, so re-importing the same document is safe and silent.

### The answer

```json
{
  "project": {"key": "shop", "name": "Shop", "description": ""},
  "mode": "merge",
  "source": "document",
  "dry_run": false,
  "changes": {
    "environments": {"created": [], "updated": [], "deleted": []},
    "unit_types":   {"created": [], "updated": [], "deleted": []},
    "attributes":   {"created": [], "updated": [], "deleted": []},
    "segments":     {"created": [], "updated": [], "deleted": []},
    "flags":        {"created": ["new_checkout"], "updated": [{"key": "dark_mode", "patch": [{"op": "replace", "path": "/configurations/production/on", "value": true}]}], "deleted": []},
    "experiments":  {"created": [], "updated": [], "deleted": []}
  },
  "warnings": []
}
```

`201` for an applied create, `200` otherwise. Each update carries an RFC 6902 JSON Patch over the entity's entry in the document; a flag is updated when its definition or any configuration changed. `warnings` are `{code, pointer, params, detail}`; the codes of a document import are `owner_replaced` and `protection_kept` (below), and a Firebase import adds [its own](#warning-codes).

## Dry runs and `If-Match`

Add `dry_run=true` to any import to see what it would do. The whole transaction runs, including every check the database makes, and is rolled back: the answer is the one the real import would give, and nothing is written, bumped or recorded.

A dry run into an existing project (merge, replace or Firebase), like an export, answers with an `ETag`: the identifier of the project's latest audit entry, quoted. A create has no project to move, so it has no `ETag` and takes no `If-Match`. Send it back as `If-Match` on the real import and the import is applied only if the project has not moved since: otherwise it is refused `412 version_mismatch` before any write, so you never apply a diff you did not see. Without the header an import applies to the project as it is.

A document that breaks a rule is refused `422 invalid_document`, whole, with `errors[{pointer, code, detail, params}]`: a JSON Pointer into the document and the code an ordinary write would use (`invalid_flag`, `invalid_configuration`, `type_immutable`, `unknown_field`, `required`, …). Every fault found is reported, so one run fixes the file.

## Protection, owners and permissions

**Protection.** An import writes protected environments directly: copying a project does not need a change request for every flag. For that reason only an owner can import, and never a project token or a member holding a lesser role. An import never changes whether an existing environment is protected: when the document disagrees, the environment keeps its value and the warning `protection_kept` names it. A new environment, and every environment of a create, takes `protected` from the document. Change requests an import moves what they were based on are superseded, as for any other change.

**Owners.** An email in `owner` resolves only to someone who may own in this project now (email compared case-insensitively). Otherwise a new flag or experiment is owned by the importing member, an existing one keeps its owner, and the warning `owner_replaced` carries the email. An override's author is the importing member when the override is new or changed.

**Permissions.** `exportProject` needs `project.read`. `importProject` and the Firebase import need `project.import`, held by owners only (scope `project:admin`, members only); creating a project from a document needs an instance admin. See [Access](access.md).

## The audit trail

An import records each entity it changes with the action a direct write records (`environment.created`, `flag.updated`, `flag.configuration_updated`, `segment.updated`, `experiment.created`, `attribute.deleted`, …), then one `project.imported` entry saying who imported what: `mode`, `source` (`document` or `firebase`) and `counts`. All carry the request's id. Because the entries are the ordinary ones, history, revert, change request superseding, [webhooks](webhooks.md) (including the `project.imported` event) and the live console treat imported changes like any other: a configuration an import wrote can be reverted to its previous version. A create records `project.created` first.

Every environment whose ruleset changed is bumped once, at the end of the import, so SDKs see the import as one change.

## Limits and refusals

| What | Limit or answer |
|---|---|
| Import body | 32 MiB, else `413 request_too_large`. |
| Entities | At most as many as a direct write allows: 5,000 flags, 1,000 segments and 20 environments, counting what the project holds that the document does not name (a merge) and judged before anything is written, else `422 invalid_document` with `too_many` at the section. |
| Media type | `application/json` or `application/yaml`, else `415 unsupported_media_type`. |
| Malformed JSON or YAML | `400 invalid_request`. |
| A document breaking a rule | `422 invalid_document`, every fault with its pointer. |
| Another `schema_version` | `422 schema_version_unsupported` with `supported_versions`. |
| Unknown project | `404`. |
| Entities | The ordinary limits: [rules, conditions, overrides and prerequisites](targeting.md#limits). |

## In the console

**Import and export** in the project navigation is open to everyone who can read the project.

- **Export** downloads the project document as JSON or YAML. The dialog that deletes a project offers the same download, beside the audit log's, because the audit log does not hold the configuration.
- **Import**, for owners, takes a `.json`, `.yaml` or `.yml` file and a mode, merge or replace. **Check what it would change** is the dry run: it lists what the import would create, update and delete, kind by kind, with the warnings, says how many entities a replace deletes, and names the protected environments it writes without a change request. **Apply** sends the dry run's `ETag` as `If-Match`, so when the project changed in between nothing is imported and the page offers to check again. A document the project already matches has nothing to apply.
- **Import from Firebase**, for owners, walks through a [Remote Config template](#importing-from-firebase-remote-config): choose the template file, tick the parameters to import, each shown with the flag key, type and tags it gets, read how each condition translates and which are skipped, choose the environment and the unit type percent rollouts use, then run a dry run of the flags, rules and attributes and apply, under the same `If-Match`.
- **Create from a document** is on the instance's **Projects** page, for instance admins: a document, a key and a name, a check of what it would create, then the project.

## The CLI

`flagnetic export`, `import` and `import-firebase` are client commands of the same binary. They call `/api/v1` with a bearer token, never the database.

| Flag | Meaning |
|---|---|
| `--url`, `FLAGNETIC_URL` | The server; `http://localhost:8080` by default. |
| `--token`, `FLAGNETIC_TOKEN`, `--token-file` | The API token. |

```bash
# Export to a file (YAML by default; --format json for JSON).
flagnetic export --project shop --output shop.yaml

# Create a project from a document, under another key.
flagnetic import --file shop.yaml --project shop-copy --name "Shop copy"

# See what a merge would do, then apply it only if nothing moved.
flagnetic import --file shop.yaml --mode merge --project shop --dry-run
flagnetic import --file shop.yaml --mode merge --project shop --if-match '"0193a8f2-7c1e-7b3d-9e4a-5f6b7c8d9e0f"'
```

- `export --project K [--format yaml|json] [--output F]`: writes to stdout unless `--output` is given (the file is written `0600`).
- `import --file F|- [--mode create|merge|replace] [--project K] [--name N] [--dry-run] [--if-match ETAG]`: a `.json` file is sent as JSON, anything else and `-` (standard input) as YAML. The mode is `create` by default; `--project` is required for merge and replace and is the new key for create; `--name` only with create.
- `import-firebase --file F --project K --environment E [--unit-type T] [--dry-run] [--if-match ETAG]`.

Results are indented JSON on standard output. Each warning goes to standard error as `warning: <code> <pointer>: <detail>`, and an export and a dry run of a merge, replace or Firebase import print `etag: <value>` there. A refusal prints `<status> <code>: <detail>`, its references, and each fault of a `422` as `  <pointer> <code>: <detail>`. The exit code is `0` on success, `1` when the server refused or the request failed, `2` for misuse (a missing flag, an unknown format, `--if-match` with create).

## Importing from Firebase Remote Config

Take a Remote Config template (the JSON that Firebase downloads) and merge it into **one environment** of a project:

```
POST /api/v1/projects/{project}/import/firebase?environment=&unit_type=anonymous&dry_run=
```

The body is the template as `application/json`; unknown template fields are ignored. `unit_type` (default `anonymous`) is the unit type percent conditions roll out on; both it and the environment must exist, else `422 invalid_document` with `unknown_environment` at `/query/environment` and `unknown_unit_type` at `/query/unit_type`. Needs `project.import`, and accepts `If-Match`. The answer is an import result plus `flags` (as stored in the environment) and `attributes` (those newly declared). Use `dry_run=true` first to see the flags, rules and attributes it would create.

Re-importing the same template changes nothing, so you can import again after editing it in Firebase.

### Parameters

| Firebase | Flagnetic |
|---|---|
| A parameter, top level or in a group | A flag with the same key; the group is a tag. |
| `valueType` unset or `STRING`, `BOOLEAN`, `NUMBER`, `JSON` | Flag type `string`, `boolean`, `number`, `json`. |
| `defaultValue` | The off and fallthrough variation. |
| `useInAppDefault`, or no value | The type's empty value, and the warning `in_app_default`. |
| Values | Variations `true` and `false` for a boolean; otherwise `v1` to `vn`, named after the condition that introduced them, equal values (by JSON meaning) shared. |
| A conditional value | A rule, in the template's condition order, described `Firebase condition "<name>"`. |

A condition expression is expanded to disjunctive normal form: one rule per conjunction, at most 50 alternatives. A configuration holds at most 50 rules; further ones are dropped with `rule_limit`.

A new flag is on in the target environment and off in the others. An existing flag (of the same type, else `type_immutable`) is switched on in the target environment, keeps its name, owner, other environments, prerequisites and overrides, gains missing variations, and has its description replaced only by a non-empty one. Rules equal to existing ones keep their ids and salts. A template cannot express a rule that serves an experiment, so the rules of the flag in the target environment that do are kept, after the translated rules, and count towards the limit of 50. An archived flag is configured and stays archived (`archived_flag`), unless the template brings a value it lacks: that would change what an archived flag serves, and is refused as `422 invalid_document` with `archived` at the parameter's pointer.

### Condition terms

| Firebase term | Becomes |
|---|---|
| `app.id` with `==`, `!=`, `exactlyMatches` | `app_id` with `eq`, `neq`, `in` |
| `device.os` | the `platform` attribute client SDKs already send |
| `app.version` comparisons and `exactlyMatches` | semver on `app_version` |
| `app.build` | `build_number` |
| `device.language in` | `locale` |
| `device.country in` | `country`, upper-cased |
| `app.userProperty['k']`, `app.customSignal['k']` | an attribute slugged from `k`: string, number for numeric comparisons, semver under `version()` |
| `contains`, `matches` | one rule per value; `notContains` is one rule |
| `percent` (`<`, `<=`, `>`, `>=`, `between`, with or without a seed) | a rollout on `unit_type` with a fresh salt |
| `true`, `false` | the literals |

Conditions Flagnetic cannot reproduce are **skipped**, with a warning naming the parameter, condition and reason: dates and times, first open, audiences, installation IDs, web elements, string operators on versions, two percent terms in one conjunction, more than 20 conditions, personalization or rollout values, and unreadable expressions. The rule is skipped, never half-translated.

### Warning codes

| Code | Pointer | Params |
|---|---|---|
| `in_app_default` | the default or conditional value | `parameter`, `condition?`, `serves` |
| `condition_skipped` | the conditional value | `parameter`, `condition`, `reason` |
| `rollout_reshuffled` | the conditional value | `parameter`, `condition`, `unit_type`, `percentage`, `firebase_percent`; a second warning with `range_start` when the range does not start at 0 |
| `attribute_declared` | the condition's expression | `parameter`, `condition`, `source`, `key`, `attribute` |
| `rule_limit` | the conditional value | `parameter`, `condition`, `max` |
| `archived_flag` | the parameter | `parameter` |

`rollout_reshuffled` is a warning on every percent condition: Firebase and Flagnetic hash units differently, so the units that were in a percentage are not the same units in the rollout. A parameter's pointer is `/parameters/<key>`, or `/parameterGroups/<group>/parameters/<key>` for a grouped parameter; its default value is `<parameter>/defaultValue` and a conditional value `<parameter>/conditionalValues/<condition>`. The condition's expression is `/conditions/<index>/expression`. A document import's own warnings are `owner_replaced {email}` and `protection_kept`.

A template fault is `422 invalid_document` with every fault reported, each with its pointer into the template: `invalid_json`, `required`, `duplicate_condition`, `unknown_condition`, `duplicate_parameter`, `invalid_value_type`, `invalid_value`, `type_immutable`, `archived`.

## The demo project

[`deploy/demo-project.yaml`](../deploy/demo-project.yaml) is a hand-written document, without ids, salts or owners: three environments (production protected), a segment, flags with a rule and a rollout, and a draft experiment. Import it into a fresh instance:

```bash
flagnetic import --file deploy/demo-project.yaml
```

The importing admin owns every flag and the experiment, and the project is `demo`. See [Installing Flagnetic](install.md#try-it-with-the-demo-project).
