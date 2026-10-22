# Flags

A **flag** is a named, typed switch defined once per project. What it is lives in its **flag definition**, the same in every environment; what it serves lives in its **flag configuration**, one per environment, each changed on its own. This page describes both through the management API (`/api/v1/projects/{project}/flags`), which the console is built on. Conditions and their operators are in [Targeting](targeting.md), reusable sets of conditions in [Segments](segments.md), and the permissions every operation needs in [Access](access.md#permissions).

## Flag definitions

`POST /flags` creates a flag; `PATCH /flags/{flag}` changes its definition for every environment at once.

| Field                 | Rule                                                                                                                                                                                                      |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `key`                 | Names the flag in code, such as `checkout.newFlow`: a letter, then letters, digits, `_`, `.` and `-`, at most 128 in all. Unique in the project, case sensitively, and never changes.                     |
| `type`                | The **flag type**: `boolean`, `string`, `number` or `json`. Never changes.                                                                                                                                |
| `name`, `description` | A name of 1 to 200 characters, a description of at most 2,000.                                                                                                                                            |
| `variations`          | The values the flag can serve, in order (see below).                                                                                                                                                      |
| `owner`               | The member accountable for the flag: an active member holding a grant in the project, or an instance admin (`invalid_owner` otherwise). It defaults to the calling member; a project token must name one. |
| `tags`                | At most 50, each of 1 to 64 characters, no two alike.                                                                                                                                                     |
| `lifetime`            | `permanent`, or `temporary` with an `expected_removal_at`. A flag created without a lifetime is temporary, and a temporary flag given no date is expected to be removed 90 days after it is created.      |
| `automatic_exposure`  | Whether SDKs record an exposure when the flag is evaluated, instead of waiting for an explicit call.                                                                                                      |

A key, type or other field that does not fit is refused `422 invalid_flag`, each violation pointing at its field; sending a new key or type is `immutable`.

### Variations

A **variation** is one of the values a flag can serve. Each has a `key` (letters, digits, `_`, `.` and `-`, 1 to 64 characters, unique in the flag), a `value` of the flag's type of at most 64 KiB as compact JSON, a `name` that defaults to the key, and a `description`. A boolean flag always has exactly the variations `true` and `false`, and is created without listing them; every other flag lists at least one.

A `PATCH` replaces the variations as a list, matched by key. Removing a variation that is still served is refused `409 in_use`, with `references` naming each flag and environment that names it: as the off or fallthrough variation, in a rule or rollout, in an override, or as the variation another flag requires as a prerequisite.

### A new flag, a new environment

A flag is created switched off in every environment of its project. A boolean flag serves `false` when off and `true` when on; any other flag serves its first variation either way until its configuration says otherwise. An environment created later gives every existing flag, archived ones included, a configuration of the same kind.

### Archive, restore, delete

`POST /flags/{flag}/archive` hides a flag from SDKs in every environment and keeps its definition and configurations; `POST /flags/{flag}/restore` brings it back as it was archived. Archiving an archived flag, or restoring one that is not, changes nothing. A flag another flag still requires as a prerequisite is not archived (`409 in_use`, naming the flags that require it).

An archived flag's definition can still be edited, but none of its configurations can be written (`409 archived`), and no condition or prerequisite may start naming it (`archived_flag`).

`DELETE /flags/{flag}` deletes a flag with its configuration in every environment, and only an archived one (`409 not_archived`), so deleting is a deliberate second step after archiving.

## Flag configurations

`GET /flags/{flag}` answers the definition with the configuration in every environment, each carrying its own version; `GET /flags/{flag}/environments/{environment}` answers one configuration and `PUT` on the same path replaces it whole.

A configuration holds:

- `on`: whether the flag is switched on in the environment.
- `off_variation`: the **off variation**, served while the flag is off and when a prerequisite fails.
- `fallthrough`: the **fallthrough variation**, served when the flag is on and no rule matches.
- `prerequisites`: other flags that must serve a given variation for this one to be evaluated.
- `overrides`: variations pinned for single units.
- `rules`: ordered rules, each with conditions and what it serves.

### How a flag is evaluated

For one flag and one context, in an environment:

1. A flag that is off serves its off variation (reason `off`).
2. The first **override** whose unit type is in the context with the same unit ID serves its variation (reason `override`).
3. The flag's prerequisites, in order: each must serve the variation it names, and the first that does not makes the flag serve its off variation (reason `prerequisite_failed`).
4. The rules, top to bottom: a rule matches when all its conditions hold, and the first that matches serves (reason `rule`, or `rollout` when its rollout includes the unit). A rollout that does not include the unit, or whose unit type the context lacks, lets evaluation continue with the next rule.
5. When no rule matched, the fallthrough variation (reason `fallthrough`).

The reference evaluator documents the full algorithm, which every SDK implements alike.

### Rules

A **rule** has an `id`, an optional `description` (at most 2,000 characters), up to 20 conditions, and a `serve`. A configuration holds at most 50 rules.

The server gives a new rule its identifier: a write sends a new rule without an `id`, and sends every rule it keeps with the `id` the server answered, so a rule keeps its identity, and its rollout its salt, however the rules are reordered or edited. An `id` that is not one of the current configuration's rules is refused `unknown_rule`, and the same `id` twice `duplicate`.

A rule serves:

- `{"kind": "variation", "variation": "on"}`: one variation to every context it matches.
- `{"kind": "rollout", "unit_type": "user", "variation": "on", "percentage": 2500}`: a **rollout**, serving the variation to a share of the units of that unit type, in basis points from 0 to 10,000 (2500 is 25%).

Experiments are not available yet: a rule serving one is refused `experiment_not_available`.

### Rollouts and salts

Which units a rollout includes is deterministic: a unit's **bucket**, from 0 to 9,999, is computed from the rollout's **salt** and the unit's ID, and the unit is in when its bucket is below the percentage. Growing the percentage therefore never moves a unit that was already in, and every SDK and the server agree on every unit.

The server gives a new rollout a salt of 16 random characters. A write omits the salt or sends it unchanged (any other is `immutable`), and changing a rule's percentage, variation or conditions keeps it. The salt changes only through `POST /flags/{flag}/environments/{environment}/rules/{rule}/regenerate-salt`, which reshuffles the rollout: units in it may fall out and others come in. A rule that is not a rollout is refused `409 not_a_rollout`.

### Overrides

An **override** pins a variation for one unit in one environment: `{unit_type, unit_id, variation, note}`. The unit type is one the project declares, the unit ID is 1 to 256 bytes, and the note, at most 200 characters once trimmed, says why. A configuration holds at most 200 overrides, one per unit type and unit ID. Each override records `added_by`, the member who last wrote it: an override a full write leaves as it was keeps its author, any other is the writer's.

Besides a full write, one override is set with `PUT /flags/{flag}/environments/{environment}/overrides/{unit_type}/{unit_id}` (`{variation, note}`) and removed with `DELETE` on the same path (`404 not_found` when the unit has none). Neither needs `If-Match`, so pinning a tester's account never conflicts with someone editing the rules, and each still moves the configuration's version on.

Override notes and authors stay on the server: SDKs receive the unit and the variation only.

### Prerequisites

A **prerequisite** requires another flag of the project to serve a given variation for the same context. A configuration lists flag-level prerequisites (`{flag, variation}`, no flag twice), and a rule may hold a condition of kind `prerequisite`. Both name a flag that is not archived and not the flag itself, and one of its variations.

In each environment, the chain of prerequisites through flag-level lists and conditions together is acyclic and at most 5 flags deep, archived flags counted. A write closing a cycle is refused `prerequisite_cycle` and one making a chain too deep `prerequisite_too_deep`, each with the flag keys of the chain in `params.path`. A flag requiring itself is the shortest cycle.

### Validation

Every write of a configuration is checked against the project before it is stored, and refused `422 invalid_configuration` with a JSON Pointer to each field to fix: variations of the flag and of the flags it requires, declared attributes and unit types, existing segments, flags that are not archived, operators and values that fit each attribute's type, prerequisite chains, and the limits (`too_many` with `params.max`).

### Switching on and off, the kill switch

A `PUT` that changes nothing SDKs receive but `on` is recorded as `flag.switched_on` or `flag.switched_off`; any other as `flag.configuration_updated`.

The **kill switch**, `POST /flags/{flag}/environments/{environment}/kill` with an optional `reason` of at most 2,000 characters, switches the flag off in the environment so it serves its off variation to everyone, and changes nothing else. It needs no `If-Match`, so it is never refused because the page it was pressed on is old, and it still moves the configuration's version on. It answers `{changed, configuration}`: a flag that is already off changes nothing and records nothing. Its audit entry `flag.killed` carries the reason and is marked as a kill switch, so the audit log lists kill switch uses with the `kill_switch` filter.

### What reaches SDKs

Each environment has a **ruleset**: its flags, segments and declarations as SDKs evaluate them, with a version that only grows. A write moves the version of every environment whose ruleset it changes: a configuration write its own environment; creating a flag, a change to its variations, automatic exposure or archival, creating or deleting a segment, a change to its conditions or private switch, and declaring or deleting a unit type or attribute, every environment of the project. Deleting an archived flag changes no ruleset, since none holds it. Changes to names, descriptions, tags, owners, lifetimes, rule descriptions and override notes change no ruleset. Archived flags, rule descriptions, override notes and authors never reach a ruleset.

## Revert

`POST /flags/{flag}/environments/{environment}/revert` with `{"version": 4}` **reverts** the flag's configuration in the environment to the version it had at that point of its history, exactly as the audit log recorded it: on or off, the off and fallthrough variations, prerequisites, overrides with their notes and authors, and rules with their identifiers and salts, so the same units fall in every rollout again.

- The version is looked up only in the history of this flag in this environment (`404 version_not_found` otherwise).
- `If-Match` names the configuration's current version, not the one reverted to.
- The configuration is validated as a replace would be, so a variation, segment, attribute or flag removed since refuses the revert, its violations pointing into the configuration it would write.
- It is a new change, recorded as `flag.reverted` with the version returned to: the history keeps both what was reverted and the revert. Reverting to what the configuration already is changes nothing and records nothing.
- It leaves the flag's definition and the project's segments as they are now.

## Copy and compare

`POST /flags/{flag}/environments/{environment}/copy` with `{"from": "staging", "include_overrides": false}` makes the flag's configuration in this environment what it is in `from`: its rules with their identifiers and salts, so the same units fall in every rollout in both environments, its prerequisites, and its off and fallthrough variations. The target keeps whether the flag is on or off, so a copy never switches it on by accident, and keeps its own overrides unless `include_overrides` asks for the source's, with their notes and authors. `If-Match` names the target's version. The copy is validated as a replace would be; copying from the environment itself is `invalid_configuration`. It is recorded as `flag.configuration_copied` with the source environment and its version.

To compare, `GET /flags?differs_between=staging,production` lists the flags whose configurations differ between two environments in what their rulesets carry: on or off, the off and fallthrough variations, prerequisites, overrides by unit type, unit ID and variation, and rules with their identifiers, conditions and serves, salts included, in order. Rule descriptions, override notes and authors, and versions are not compared. Two environments that differ only there list nothing.

## Listing flags

`GET /flags` lists the project's flags, each with its **flag state** in every environment: `{environment, on, has_rules, has_rollout}`, and its `updated_at`.

| Parameter                 | Lists                                                                                                     |
| ------------------------- | --------------------------------------------------------------------------------------------------------- |
| `q`                       | Flags whose key or name contains the text, whatever its case.                                             |
| `type`, `tag`, `owner`    | Flags of that type, carrying that tag, owned by that member.                                              |
| `archived`                | The archived flags instead of the others.                                                                 |
| `environment` and `state` | Flags whose configuration in that environment is `on`, `off`, has `rules`, or has a `rollout`.            |
| `differs_between`         | Flags whose configurations differ between two environments (above).                                       |
| `sort`                    | By `key` (the default) or `name` in byte order, or by `updated`, the last change to the definition first. |

Pages hold `limit` flags; `next_cursor` fetches the next page under the same `sort`.

In the console, **Flags** in the project navigation lists the project's flags with their type, their state in every environment (on or off, whether it has rules, whether a rule serves a rollout), owner, tags and last update, filtered by text, type, tag, owner, archived, state in the chosen environment and **Differs between** two environments, sorted by key, name or last update, the filters, the order and the page kept in the address so a view can be shared. The list opens in the environment the address names, else the one this browser showed last in the project, else the first; a flag opens in that environment's tab. **New flag**, offered to a member holding `flag.edit`, creates a flag of any type: a boolean flag is created with its `true` and `false`, any other starts with two variations to fill in, and the owner is the member creating it unless they choose another member of the project. Creating it opens the flag's page.

A flag's page has one tab per environment and a History tab. **Edit definition** edits the definition for every environment; archiving, restoring and deleting (an archived flag only, typing its key) sit beside it, and a refusal names what still requires the flag. An environment's tab switches the flag on or off there and adds or removes overrides, each saved at once; while the flag is on, **Kill switch** switches it off after a reason of at least 10 characters, showing who used it last and why. If someone saved the definition while it was open, saving shows who and when with a diff of their change, and offers to reload, losing the edits, or to keep the edits and save them onto the newer version.

The rest of an environment's tab is a draft, laid out in the order evaluation reads it: the off variation, the overrides, the prerequisites, the rules and the fallthrough. Each rule holds up to 20 conditions on an attribute (the operators and value inputs follow the attribute's type: lists as chips, full semantic versions, dates and times), on a segment, or on another flag's variation, and serves a variation or a rollout, whose salt is shown and is regenerated only on request, after a warning. Rules reorder by dragging, with their arrow buttons, or with Alt+Arrow Up and Down. **Save** replaces the configuration under the version the draft was loaded at; leaving the tab with a draft asks first; a refusal is shown on the rule and condition it names. If someone saved the configuration meanwhile, saving shows who and when with a diff of their change, and offers to reload and reapply the draft (rules matched by identifier; new cards, and edited cards whose rule they removed, are appended and marked) or to discard it.

The History tab lists every change to the flag, its definition and its configuration in every environment, newest first; an entry opens in a drawer with what it changed, and one that changed a configuration offers **Revert to this version** to a member holding `flag_configuration.revert`, confirming against the version the configuration is at now. **Compare environments** shows the flag's configurations in two environments side by side, marking every part SDKs would receive differently, and **Copy here** copies one into the other as the copy operation above does, the target staying on or off, its overrides replaced only on request.

## Trying a configuration

`POST /environments/{environment}/evaluate` evaluates flags for a context as the environment's SDKs would, with the full reason of each result: the served value and variation, the reason, the rule that served, an error code, and for a rollout the unit's bucket and the rollout's salt. It evaluates every flag that is not archived, or those `flags` names. With a `draft` (`{flag, configuration}`) it evaluates that flag's unsaved configuration in place of its saved one, validated as a replace would be and never stored. It answers the environment's `ruleset_version` beside the results (every write that changes a ruleset moves its version to the greater of the previous version plus one and the server clock in milliseconds, so versions only increase and a version is never handed out twice, even after a database failover or restore that lost recent writes), needs only `project.read`, and records no exposure.

```json
{
  "context": {
    "units": { "user": "u-1001" },
    "attributes": { "plan": "pro", "app_version": "2.4.0" }
  },
  "flags": ["new_checkout"]
}
```

A unit ID over 256 bytes is refused `422 invalid_context`.

In the console, **Tester** on a flag's page (or the `t` key) opens a panel beside the environment's tab that evaluates the flag there for a context of units and typed attributes: against the saved configuration, or with **Use unsaved draft** against the draft being edited, which is never saved. It shows the value, the variation, the reason, the rule that served (marked in the editor), for a rollout the unit's bucket and the salt, and whether an override decided. The last 10 contexts evaluated in a project are kept in this browser to pick again. The tester needs only read access, so a viewer can answer why a user sees what they see.

## Editing at the same time

Every flag definition and every flag configuration carries a `version`, which counts its accepted changes from 1 and is answered as an `ETag` (`"7"`) on every read of it and every write. Every update, replace, delete, archive, restore, revert, copy and salt regeneration names the version it was based on in `If-Match` (`"7"` or `7`); the kill switch and the writes of a single override need none. A write without it is refused `428 precondition_required`, and one based on a version that moved on `412 version_mismatch`, whose `current_version` is the version now: two people editing the same flag never overwrite each other unseen. Both are judged after the request is validated, so a write that is invalid as well says what to fix first. A write that changes nothing stores nothing, keeps the version and records nothing, and answers the entity as it is. Segments, projects, environments, unit types and attributes behave the same way.

## History

Every accepted write records one audit entry, with the whole definition or configuration, version included, before and after it; this is what revert reads. Flags record `flag.created`, `flag.updated`, `flag.archived`, `flag.restored` and `flag.deleted`, and per environment `flag.switched_on`, `flag.switched_off`, `flag.configuration_updated`, `flag.override_set`, `flag.override_removed`, `flag.salt_regenerated`, `flag.killed`, `flag.reverted` and `flag.configuration_copied`. A flag's history across its environments is `GET /projects/{project}/audit?entity_id={flag id}` (see [The audit log](access.md#the-audit-log)).
