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

## Starting an experiment

`POST /projects/{project}/experiments/{experiment}/start` (`experiment.edit`, with `If-Match`) starts a draft. In the same transaction the ruleset of every environment holding a rule that serves the experiment moves on to a new version carrying it, so the experiment begins in all of them at once; the rulesets of the other environments do not move. An experiment no environment holds starts all the same, and reaches an environment when a rule there serves it.

A draft missing anything a running experiment needs is refused `422 experiment_not_ready`, with one violation per missing item, so they can all be fixed at once:

| Pointer | Code | When |
|---|---|---|
| `/flag` | `flag_archived` | The flag is archived. |
| `/unit_type` | `undeclared_unit_type` | The unit type is no longer declared. |
| `/variations` and below | the codes of a create | The weights are no longer valid against the flag. |
| `/hypothesis` | `required` | The experiment has no hypothesis. |
| `/expected_end_at` | `required` | The experiment has no expected end date. |

Only a draft starts (`409 not_draft`): there is no restart, and a stopped experiment is rerun as a [clone](#cloning) with new salts. There is no pause either: the flag's kill switch is the pause.

## A running experiment

While an experiment runs, its rules carry what decides each unit's variation: the unit type, both salts, the traffic allocation and the weights in the experiment's order. Its flag, unit type and weights are frozen: a `PATCH` changing any of them is refused `409 frozen`, with a `frozen` violation on each of `/flag`, `/unit_type` and `/variations` it would change (naming one with the value it has already changes nothing). The name, hypothesis, owner, tags and expected end date stay editable, and change no ruleset.

The traffic allocation only grows: a smaller one is refused `409 traffic_allocation_decrease`. Growing it moves on the ruleset of every environment holding the experiment, and includes more units without moving any unit already included, since the inclusion bucket of a unit never changes. It is recorded as `experiment.traffic_allocation_changed`.

`POST /projects/{project}/experiments/{experiment}/salts` (`experiment.edit`) draws a new inclusion salt and variation salt, again apart from every salt of the project's experiments and rollouts. `DELETE /projects/{project}/experiments/{experiment}` (`experiment.delete`, maintainers and owners) deletes the experiment. Regenerating salts and deleting are for drafts only: any other status is refused `409 not_draft`; past the draft, editing follows [what a running experiment allows](#a-running-experiment).

## Cloning

`POST /projects/{project}/experiments/{experiment}/clone` (`experiment.edit`) copies an experiment of any status into a new draft. The body names the clone's `key` and, optionally, its `name` (the source's otherwise). The hypothesis, tags, flag, unit type, weights, traffic allocation and expected end date are copied; the salts are new, so a rerun never reuses an assignment; there is no decision. The owner is the calling member, or the source's owner for a project token. The clone is judged as a create is. The source is not changed, so cloning takes no `If-Match`.

## Salts

Every experiment gets two salts when it is created: the `inclusion_salt` decides whether a unit is included in the traffic allocation, the `variation_salt` decides which variation an included unit gets. Each is 16 lower-case hexadecimal characters, and the server draws them so that no other experiment or [rollout](flags.md) of the project uses either one: two experiments, or an experiment and a rollout, therefore assign units independently of each other. Both are public: the experiment shows them so an analyst can recompute any unit's buckets.

## Assignment

`GET /projects/{project}/experiments/{experiment}/assignment?unit_id=…` (`project.read`) answers how the experiment treats one unit, from its own fields in whatever status it is: both salts, both buckets, the traffic allocation, whether the unit is `included` and, if so, its `variation`. A bucket is `murmur3_32("{salt}.{unit_id}") mod 10000`. A unit is included when its inclusion bucket is below the traffic allocation; an included unit gets the variation whose range of cumulative weights, in the experiment's order, holds its variation bucket. The variation bucket is answered for a unit left out too: it is the bucket the unit would be assigned by should the allocation grow to include it. The answer's `sql` is a ClickHouse query recomputing all of it, ready to run:

```sql
SELECT
    unit_id,
    murmurHash3_32(concat('a3f9c2e17b4d0e58', '.', unit_id)) % 10000 AS inclusion_bucket,
    murmurHash3_32(concat('7e1d4c90b2a6f3d1', '.', unit_id)) % 10000 AS variation_bucket,
    inclusion_bucket < 6000 AS included,
    if(included, multiIf(variation_bucket < 3000, 'control', variation_bucket < 10000, 'treatment', NULL), NULL) AS variation
FROM (SELECT 'user-42' AS unit_id)
```

Replace the subquery with a table holding a `unit_id` column to recompute every unit's assignment in the warehouse. The lookup reads no event and records nothing. A `unit_id` that is missing, empty or over 256 bytes is answered 400.

## The uniformity check

Before the first experiment, check that the bucketing hash spreads your own unit IDs evenly. `POST /projects/{project}/hash-check` (`project.read`) takes 1,000 to 200,000 `unit_ids` (each 1 to 256 bytes; duplicates count as often as they occur), an optional `salt` (a random one in an experiment salt's form otherwise), an optional `second_salt` and `bins` (2 to 10,000, default 100). It groups the 10,000 buckets into `bins` bins of consecutive buckets (bucket `b` falls into bin `b × bins / 10000`), and compares each bin's count with what a uniform hash expects of its width by Pearson's chi-square test with `bins − 1` degrees of freedom: the answer gives the counts, their `min` and `max`, `chi_square`, `df`, `p_value` and a `verdict`, `uniform` when the p-value is at least 0.001 and `not_uniform` otherwise. With a `second_salt` it also tests that both salts' buckets are independent, as an experiment's inclusion and variation buckets must be: Pearson's test of independence on the table of both salts' bins, over its non-empty rows and columns, whose `verdict` is `independent`, `dependent`, or `untestable` when every unit ID fell into one bin under one of the salts. A request the check cannot run on is answered 422 `invalid_uniformity_check`, naming each field. The check records nothing.

The same check runs offline, so unit IDs never have to leave your machine:

```bash
flagnetic hash-check --second-salt 7e1d4c90b2a6f3d1 user-ids.txt
```

It reads one unit ID per line from the file, or from standard input without one or with `-`, takes `--salt`, `--second-salt` and `--bins` as the API takes them, needs neither a database nor a configuration, and prints the API's answer as JSON. A list it cannot check exits 1, naming each line or flag at fault on standard error.

## In the console

**Experiments** in the project navigation lists the project's experiments with their status, flag, owner, tags, expected end and holding environments: the environments whose rules serve the experiment, read from its flag. The list filters by status, flag, owner and tag, includes archived experiments on request, and pages forward and back; the filters are kept in the address. **New experiment**, offered to a member holding `experiment.edit`, creates a draft.

The form takes the key, name, hypothesis, flag, unit type, weights, traffic allocation, expected end date, owner and tags. Choosing a flag fills the weights with its variations split evenly; weights and the traffic allocation are typed as percentages with at most two decimals, and the total is shown as they are typed. The form saves only what could start: weights summing to exactly 100.00 %, a hypothesis and an expected end date, though the API accepts a draft without the last two. Fields the experiment's status no longer lets change are shown disabled with the reason. On an experiment's page, **Save** sends what changed under the version the page was opened at; if someone saved the experiment meanwhile, saving shows who and when with a diff of their change, and offers to reload, losing the edits, or to keep the edits and save them onto the newer version.

## Audit

Every write records one entry (see [The audit log](access.md#the-audit-log)): `experiment.created` and `experiment.cloned` with the experiment after it (a clone also names the experiment it was copied from), `experiment.updated`, `experiment.salts_regenerated`, `experiment.started` and `experiment.traffic_allocation_changed` (a change of a running experiment's allocation, whatever else the same `PATCH` changed) with the experiment before and after, and `experiment.deleted` with the experiment before.
