# Experiments

An experiment compares two or more variations of one [flag](flags.md) on units of a declared unit type, to learn which does better. It has a hypothesis written before it runs, an owner, the weights that split the traffic, a traffic allocation and two salts. Creating one only describes the comparison: it starts as a draft, which changes nothing any SDK receives.

## Creating an experiment

`POST /projects/{project}/experiments` needs the `experiment.edit` permission (token scope `experiments:write`) and takes:

| Field | |
|---|---|
| `key` | Names the experiment in rules and in the exposures SDKs report. Starts with a letter, then letters, digits, dots, hyphens and underscores, at most 128 characters. Unique, case sensitively, in the project (`409 already_exists`), and never changes. |
| `name` | 1 to 200 characters. |
| `hypothesis` | What the experiment expects to find, at most 2,000 characters. Optional here, required to start. |
| `owner` | An active member holding a grant in the project, or an instance admin; the calling member when absent. A project token has to name one. |
| `tags` | At most 50, each 1 to 64 characters. |
| `flag` | The key of a flag of the project that is not archived. |
| `unit_type` | A unit type the project declares. |
| `variations` | 2 to 50 distinct variations of the flag, each with a `weight` in basis points (0.01 %), the weights summing to exactly 10,000. Their order is kept: it fixes the cumulative bucket ranges. |
| `traffic_allocation` | The share of units included, 0 to 10,000 basis points; every unit's (10,000) when absent. |
| `expected_end_at` | A calendar day. Optional here, required to start. |

A request breaking several rules is refused `422 invalid_experiment` with one violation per field to fix, each naming its JSON Pointer, such as `/variations/1/variation` `unknown_variation`.

The answer is the experiment at `status` `draft` and `version` 1, which is also its `ETag`. `GET /projects/{project}/experiments/{experiment}` reads it again.

## Listing experiments

`GET /projects/{project}/experiments` lists the project's experiments by key in byte order (capitals before lower case), paged with `limit` (1 to 200, 50 by default) and `cursor`, the `next_cursor` of the previous page. Filters, all of which apply when given: `status` (`draft`, `running`, `stopped` or `archived`), `flag` (a flag's key), `owner` (a member's identifier) and `tag` (matched exactly). Archived experiments are left out unless `include_archived` is `true` or `status` is `archived`. Keep the same filters while paging.

## Changing a draft

Every write to an existing experiment names the version it is based on in `If-Match` (the `ETag` of the last answer, such as `"3"`): without it the write is refused `428 precondition_required`, and with an older one `412 version_mismatch`, which names the current version.

A draft is edited in full with `PATCH /projects/{project}/experiments/{experiment}` (`experiment.edit`): name, hypothesis, owner, tags, flag, unit type, variations, traffic allocation and expected end date, each judged as a create judges it. Fields left out stay as they are; the key never changes (`422 invalid_experiment`, `immutable` on `/key`). A change to the values the experiment already has changes nothing: the version stays and nothing is recorded. Changing the flag or the variations judges the variations against the flag as it will be.

`POST /projects/{project}/experiments/{experiment}/salts` (`experiment.edit`) draws a new inclusion salt and variation salt, again apart from every salt of the project's experiments and rollouts. `DELETE /projects/{project}/experiments/{experiment}` (`experiment.delete`, maintainers and owners) deletes the experiment. Editing, regenerating salts and deleting are for drafts only: any other status is refused `409 not_draft`.

## Cloning

`POST /projects/{project}/experiments/{experiment}/clone` (`experiment.edit`) copies an experiment of any status into a new draft. The body names the clone's `key` and, optionally, its `name` (the source's otherwise). The hypothesis, tags, flag, unit type, weights, traffic allocation and expected end date are copied; the salts are new, so a rerun never reuses an assignment; there is no decision. The owner is the calling member, or the source's owner for a project token. The clone is judged as a create is. The source is not changed, so cloning takes no `If-Match`.

## Salts

Every experiment gets two salts when it is created: the `inclusion_salt` decides whether a unit is included in the traffic allocation, the `variation_salt` decides which variation an included unit gets. Each is 16 lower-case hexadecimal characters, and the server draws them so that no other experiment or [rollout](flags.md) of the project uses either one: two experiments, or an experiment and a rollout, therefore assign units independently of each other. Both are public: the experiment shows them so an analyst can recompute any unit's buckets.

## Audit

Every write records one entry (see [The audit log](access.md#the-audit-log)): `experiment.created` and `experiment.cloned` with the experiment after it (a clone also names the experiment it was copied from), `experiment.updated` and `experiment.salts_regenerated` with the experiment before and after, and `experiment.deleted` with the experiment before.
