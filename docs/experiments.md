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

The answer is the experiment at `status` `draft` and `version` 1, which is also its `ETag`. `GET /projects/{project}/experiments/{experiment}` reads it again, and `GET /projects/{project}/experiments` lists the project's experiments by key in byte order (capitals before lower case), paged with `limit` (1 to 200, 50 by default) and `cursor`, the `next_cursor` of the previous page.

## Salts

Every experiment gets two salts when it is created: the `inclusion_salt` decides whether a unit is included in the traffic allocation, the `variation_salt` decides which variation an included unit gets. Each is 16 lower-case hexadecimal characters, and the server draws them so that no other experiment or [rollout](flags.md) of the project uses either one: two experiments, or an experiment and a rollout, therefore assign units independently of each other. Both are public: the experiment shows them so an analyst can recompute any unit's buckets.

## Audit

Creating an experiment records `experiment.created` with the experiment after it (see [The audit log](access.md#the-audit-log)).
