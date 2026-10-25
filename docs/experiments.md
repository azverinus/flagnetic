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

Only a draft starts (`409 not_draft`; `409 archived` for an archived experiment): there is no restart, and a stopped experiment is rerun as a [clone](#cloning) with new salts. There is no pause either: the flag's kill switch is the pause.

## A running experiment

While an experiment runs, its rules carry what decides each unit's variation: the unit type, both salts, the traffic allocation and the weights in the experiment's order. Its flag, unit type and weights are frozen: a `PATCH` changing any of them is refused `409 frozen`, with a `frozen` violation on each of `/flag`, `/unit_type` and `/variations` it would change (naming one with the value it has already changes nothing). The name, hypothesis, owner, tags and expected end date stay editable, and change no ruleset. The hypothesis is never emptied once the experiment has started (`422 invalid_experiment`, `required` on `/hypothesis`): only a draft may be without one.

A context evaluated in the tester (`POST /projects/{project}/environments/{environment}/evaluate`) that a running experiment includes is answered reason `experiment` with an `experiment` block: the experiment's key, the variation assigned, the unit type and unit, both salts and both buckets, and whether an override decided the result (an override shows the assignment it replaced). A unit outside the traffic allocation, or a context without the experiment's unit type, goes on to the next rule.

The traffic allocation only grows: a smaller one is refused `409 traffic_allocation_decrease`. Growing it moves on the ruleset of every environment holding the experiment, and includes more units without moving any unit already included, since the inclusion bucket of a unit never changes. It is recorded as `experiment.traffic_allocation_changed`.

`POST /projects/{project}/experiments/{experiment}/salts` (`experiment.edit`) draws a new inclusion salt and variation salt, again apart from every salt of the project's experiments and rollouts. `DELETE /projects/{project}/experiments/{experiment}` (`experiment.delete`, maintainers and owners) deletes the experiment. Regenerating salts and deleting are for drafts only: a running or stopped experiment is refused `409 not_draft`, an archived one `409 archived`; past the draft, editing follows [what a running experiment allows](#a-running-experiment) and [what a stopped one allows](#stopping-with-a-decision).

## Stopping with a decision

`POST /projects/{project}/experiments/{experiment}/stop` (`experiment.edit`, with `If-Match`) stops a running experiment with a decision: `{"outcome": "winner", "winner_variation": "short", "note": "Short titles won."}` or `{"outcome": "removed"}`. In the same transaction, every rule serving the experiment, in every environment, is rewritten:

- With `winner`, each rule serves the winning variation instead, keeping its identifier, description, conditions and place, so the winner reaches everyone the experiment was served to at once.
- With `removed`, each rule is deleted, and the flag goes on to its other rules.

Each configuration rewritten is a configuration write of its own: its version moves on, it is recorded as `flag.configuration_updated` with `experiment_stopped` naming the experiment, and the ruleset of its environment moves on. No other ruleset moves. The experiment is recorded as `experiment.stopped` and answers its `decision`: `outcome`, `winner_variation` (null for a removal) and `note` (empty for none).

A decision that is not valid is refused `422 invalid_decision`: a winner naming no variation (`required` on `/winner_variation`) or one the experiment does not compare (`unknown_variation`), a removal naming a winner (`invalid_value`), a note over 2,000 characters (`too_long` on `/note`). An unknown `outcome` is `400 invalid_request`. Only a running experiment stops: a draft or a stopped one is refused `409 not_running`.

A stopped experiment keeps its name, hypothesis (never emptied), owner and tags editable; its flag, unit type, weights, traffic allocation and expected end date are refused `409 frozen`, a violation per field. `PUT /projects/{project}/experiments/{experiment}/decision` (`experiment.edit`, with `If-Match`) sets the decision's note, `{"note": ""}` clearing it, recorded as `experiment.updated`; the outcome and the winner never change. Only a stopped experiment has a decision to note (`409 not_stopped`).

The flag of a stopped experiment can be archived again, but a variation the experiment compared still cannot be removed from it: the decision and the exposures counted keep naming it.

## Archiving

`POST /projects/{project}/experiments/{experiment}/archive` (`experiment.edit`, with `If-Match`) archives a draft or a stopped experiment, recorded as `experiment.archived`; no ruleset moves. A running experiment is stopped with a decision first (`409 running`). An archived experiment is left out of the list unless asked for, and every write of it, archiving it again included, is refused `409 archived`; it can still be [cloned](#cloning).

## Reverts and copies keep experiment rules

A [revert or a copy](flags.md) writes a whole configuration, but whether an experiment runs is decided on the experiment. Either is refused `409 experiment_rules_changed` when it would add or remove a rule serving an experiment, or change which experiment a rule serves: a copy would put an experiment into an environment nobody chose, a revert across a start would starve it, and a revert across a stop would serve again what no longer runs in place of the decision. Everything around the experiment's rules may still be reverted or copied; an experiment reaches or leaves an environment only through an explicit rule edit.

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

An experiment's page also shows its [exposures](#exposures), one environment per tab, opening on the first environment whose rules serve it: per variation the units first exposed, their share against the share the weights ask for, the exposures and the overridden ones; per UTC day the same counts with a bar of the day's shares against the weights; the exposures refused, by reason, when there are some; and the latest conflicting units. The counts say when the rollup last moved, and **Refresh** reads them again. **Assignment lookup** takes a unit ID and shows its buckets, whether it is included and its variation, with the ClickHouse query to copy (see [Assignment](#assignment)).

## Exposures

SDKs report an exposure each time a unit is assigned a variation of an experiment (see [Events](sdks/events.md)). Every 10 seconds the server rolls the exposures it stored up into what three reads answer, in one environment each (`project.read`):

- `GET /projects/{project}/experiments/{experiment}/exposures?environment=` lists, per variation, the `exposures`, the `overridden` exposures and the `first_exposures`, the variation's share of first exposures and the share its weight asks for; the experiment's variations come first, in its order, then any other variation an override exposure named. It also counts the `conflicts` (exposures, and units having one) and the exposures refused, by reason, and says in `as_of` when the rollup last moved.
- `GET …/exposures/timeline?environment=&from=&to=` gives the same counts per UTC day and variation, `from` and `to` being days, both included and both optional.
- `GET …/conflicts?environment=` lists up to 100 units exposed to another variation than their first exposure's, the most recent conflict first.

A unit's first exposure is its exposure with the earliest `occurred_at`, ties broken by the smaller `event_id`, so an exposure arriving late from a phone that was offline replaces it when it occurred earlier. Each other exposure of the unit naming another variation is a conflict, and a unit's conflicting variation is the other one it was last exposed to by `occurred_at`. Override exposures are counted as overridden and never make, replace or conflict with a first exposure: an override decides the variation whatever the assignment. Exposures and overridden exposures count on the day they were received, first exposures on the day they occurred, so a replacement moves a first exposure to its own day. Whatever order the exposures arrive in, and however SDKs batch them, the counts come out the same.

## The sample-ratio check

Every 10 minutes the server checks each running experiment in each environment whose configuration holds its rule, and once more when it stops, in the environments that held its rule until the stop rewrote them. It compares the first exposures of the variations the experiment weighs with what the weights ask for: with `N` the first exposures, `O` a variation's and `E = N × weight / 10000` its expected count, `X² = Σ (O − E)² / E` with one degree of freedom fewer than there are variations, and the p-value is the upper tail of the chi-square distribution. Weights 5,000/5,000 and first exposures 5,200 and 4,800 give `X²` 16 and a p-value of about 0.000063.

The check is skipped below 1,000 first exposures (`below_minimum_sample`) or when a variation expects fewer than 5 (`expected_below_5`), and then leaves any alert as it is. Below a p-value of 0.001 it raises an `srm` alert in the environment; above 0.01 it clears it; between the two the alert stays as it is, so it does not flap while the p-value hovers around a threshold. Growing the traffic allocation never moves an included unit, so it raises nothing.

`GET …/exposures?environment=` answers the latest check in `sample_ratio` (`n`, `chi_square`, `p_value`, `skipped_reason`, `active`, `checked_at`), null before the first. A stopped experiment keeps the check its stop took and is never checked again, though its late exposures are still counted.

## Alerts

`GET /projects/{project}/experiments/{experiment}/alerts` lists every alert the experiment has had, the most recently raised first; `active=true` keeps those not cleared, `active=false` those cleared. Each names its `kind`, its `environment`, when it was `raised_at`, `cleared_at` and `acknowledged_at`, and in `details` what the check found when it raised it. At most one alert of a kind is active per environment; once it clears, a mismatch returning raises a new one. An `srm` alert the final check raises stays active on the stopped experiment.

`POST …/alerts/{alert}/acknowledge` (`experiment.edit`) hides an alert until it clears. It takes no `If-Match`, since an alert has no version; acknowledging again answers the alert as it is.

## Audit

Every write records one entry (see [The audit log](access.md#the-audit-log)): `experiment.created` and `experiment.cloned` with the experiment after it (a clone also names the experiment it was copied from), `experiment.updated` (a decision's note included), `experiment.salts_regenerated`, `experiment.started`, `experiment.stopped`, `experiment.archived` and `experiment.traffic_allocation_changed` (a change of a running experiment's allocation, whatever else the same `PATCH` changed) with the experiment before and after, and `experiment.deleted` with the experiment before. The checks record `experiment.alert_raised` and `experiment.alert_cleared` with the alert, made by the system, and the first acknowledgement records `experiment.alert_acknowledged` with the alert before and after.
