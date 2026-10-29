# The sample-ratio check

A sample-ratio mismatch is an experiment whose units do not reach its variations in the proportions its weights ask for. The hash splits units evenly (the [uniformity check](experiments.md#the-uniformity-check) shows it on your own IDs), so a mismatch nearly always means exposures are lost or added for one variation on the way: a redirect or a crash before the exposure is recorded, an exposure recorded on one branch of the code only, a bot filtered from one variation. Whatever the experiment then measures compares two populations that differ in more than the variation, and its result cannot be trusted. Flagnetic checks every running experiment for it, and this page shows how, so that you can reproduce the check from the exposures in your warehouse.

## When it runs

- Every 10 minutes, for each running experiment, in each environment whose flag configuration holds a rule serving it. Each environment is judged on its own: a skew in staging never raises an alert in production.
- When the experiment stops, in the environments that held its rule until the stop rewrote them. That check counts what the exposure rollup has read by then, which can be less than the experiment received: exposures accepted a moment before the stop may still wait for the rollup. So it is not final (`final` is false), and a round of the checks completes it, see [The final check](#the-final-check).

Each experiment is checked in a transaction of its own, so one experiment slow to check never holds back or undoes the others.

## The final check

A stopped experiment is checked once more, over every exposure it accepted before the stop. The stop cannot wait for that: the exposure rollup can lag for as long as a transaction stays open on the database, and the stop holds the project's lock while it rewrites the rules. So the stop takes its own check at its time, marked not final, and the next rounds of the checks leave it as it is until a minute has passed since the stop, so that an exposure still being written when it happened is in, and the rollup has read every exposure received at or before the stop (the oldest exposure the rollup has not read yet, if any, was received after it). The first round to find so then:

- counts the first exposures among the exposures received at or before the stop, whatever the rollup has read since: an exposure received after the stop stays out, though it is counted in the experiment's exposures. An experiment that started before the raw exposures' retention (`FLAGNETIC_EVENTS_RETENTION_DAYS`) may have lost some of them, so its final check counts what the rollup holds instead, which includes exposures received after the stop that the rollup had read by then;
- stores the result in each environment the stop checked, final, with `checked_at` the round's time;
- raises or clears the `srm` alert as a running check does, so an alert the stop raised on partial counts is cleared when the whole of the exposures turns out to be in line.

Nothing replaces a final result afterwards, and an experiment archived before its final check is still given it. Until then the console says the final check is pending.

## What it counts

Only first exposures count: each unit's earliest exposure in the experiment and environment by `occurred_at`, ties broken by the smaller `event_id`, override exposures left out (see [Exposures](experiments.md#exposures)). A unit counts once whatever it was exposed to later, so conflicts do not move the check, and override exposures, which QA sends to see a variation whatever the assignment, never do either. Only the variations the experiment weighs are counted. The counts are the exposure rollup's, so the check sees exposures some seconds after they are received; a stopped experiment's final check instead reads the stored exposures received at or before the stop, applying the same rule.

## The statistic

For an experiment weighing `k` variations, each `i` with weight `wᵢ` in basis points (the weights sum to 10,000):

- `N`: the first exposures of all `k` variations;
- `Oᵢ`: the first exposures of variation `i`;
- `Eᵢ = N × wᵢ / 10000`: the first exposures its weight expects;
- `X² = Σ (Oᵢ − Eᵢ)² / Eᵢ`, Pearson's chi-square statistic, with `k − 1` degrees of freedom;
- `p`: the probability of an `X²` at least this large were the split right, the upper tail of the chi-square distribution with `k − 1` degrees of freedom, `Q((k − 1) / 2, X² / 2)` with `Q` the regularised upper incomplete gamma function. In SciPy that is `scipy.stats.chi2.sf(x2, k - 1)`, in R `pchisq(x2, k - 1, lower.tail = FALSE)`.

## When it is skipped

The check computes nothing, and leaves any alert as it is, when:

- `N` is below 1,000 (`below_minimum_sample`): a smaller sample is too noisy to judge a split;
- some `Eᵢ` is below 5 (`expected_below_5`): the chi-square distribution no longer approximates `X²` well there.

A variation weighed 0 expects no unit at all, so an experiment weighing one is always skipped: as `below_minimum_sample` below 1,000 first exposures, as `expected_below_5` from then on.

## Alerts and hysteresis

Below a p-value of 0.001 the check raises an `srm` alert in the environment; above 0.01 it clears the active one; between the two, it leaves the alert as it is. With a single threshold, a p-value hovering around it would raise and clear an alert every 10 minutes; with two, an alert is raised only by strong evidence of a mismatch and cleared only once the evidence is gone. At most one `srm` alert is active per experiment and environment; acknowledging it hides its banner until it clears, and a mismatch returning after a clear raises a new one.

The stop's check and the final check raise and clear like any other. An `srm` alert still active after the final check stays active on the stopped experiment, since nothing checks it again: it is the record of how the split ended. See [Alerts](experiments.md#alerts) for the alert resource and the other kinds.

Growing the traffic allocation never raises a false alert: a unit's variation comes from its variation bucket, which does not depend on its inclusion bucket, so the units an allocation adds split by the same weights as the units already in.

## Reading it

`GET /api/v1/projects/{project}/experiments/{experiment}/exposures?environment=` answers the latest check in that environment as `sample_ratio`, null before the first:

| Field | |
|---|---|
| `n` | `N`. |
| `chi_square`, `p_value` | `X²` and `p`; null when the check was skipped. |
| `skipped_reason` | `below_minimum_sample`, `expected_below_5`, or null. |
| `active` | Whether an `srm` alert is active in the environment, acknowledged or not. |
| `final` | Whether this is the result the experiment keeps. True for every result of a running experiment; false for the stop's own until the final check replaces it. |
| `checked_at` | When the check ran; the stop's time for the stop's own check, the round's for the final one. |

An `srm` alert carries the check that raised it in `details.sample_ratio`.

## A worked example

An experiment weighs `control` and `treatment` 5,000 each, and 10,000 units were first exposed in production, 5,200 to `control` and 4,800 to `treatment`:

- `N = 10000`, so `E = 10000 × 5000 / 10000 = 5000` for each variation; neither is below 5, and `N` is not below 1,000, so the check runs.
- `X² = (5200 − 5000)² / 5000 + (4800 − 5000)² / 5000 = 8 + 8 = 16`, with `2 − 1 = 1` degree of freedom.
- `p = Q(0.5, 8) ≈ 0.000063`, below 0.001: an `srm` alert is raised in production.

A split of 52 % to 48 % looks harmless, but over 10,000 units it is 200 units off on each side, and a fair split leaves one that far apart in about 6 experiments in 100,000. With 5,050 and 4,950 instead, `X² = 1` and `p ≈ 0.32`: nothing is raised, and an `srm` alert already active would be cleared.

## Reproducing it in a warehouse

The check needs nothing but the exposures. Over a table holding the exposure events your SDKs recorded, each with the fields of an [exposure](sdks/events.md) (`experiment_key`, `variation_key`, `unit_id`, `occurred_at`, `event_id`, `override`) and the `environment` it was recorded in, this ClickHouse query recomputes it for one experiment in one environment; list the experiment's weights in `weights`:

```sql
WITH
    weights AS (
        SELECT * FROM values('variation String, weight UInt32', ('control', 5000), ('treatment', 5000))
    ),
    first_exposures AS (
        SELECT unit_id, argMin(variation_key, (occurred_at, event_id)) AS variation
        FROM exposures
        WHERE environment = 'production' AND experiment_key = 'checkout_title_test' AND NOT override
        GROUP BY unit_id
    ),
    observed AS (
        SELECT w.variation AS variation, w.weight AS weight, countIf(f.unit_id != '') AS o
        FROM weights AS w LEFT JOIN first_exposures AS f ON f.variation = w.variation
        GROUP BY w.variation, w.weight
    ),
    total AS (SELECT sum(o) AS n FROM observed)
SELECT
    n,
    groupArray((variation, o)) AS first_exposures,
    sum(pow(o - n * weight / 10000, 2) / (n * weight / 10000)) AS chi_square,
    count() - 1 AS df,
    n < 1000 OR min(n * weight / 10000) < 5 AS skipped,
    if(df = 1, erfc(sqrt(chi_square / 2)), nan) AS p_value
FROM observed CROSS JOIN total
GROUP BY n
```

`first_exposures` keeps each unit's earliest non-override exposure; `observed` counts them per weighed variation, a variation nobody reached counting 0. With two variations, one degree of freedom, the p-value is `erfc(√(X² / 2))`, which the query computes; with more, take `chi_square` and `df` to a statistics library, such as `scipy.stats.chi2.sf(chi_square, df)`. Keep `event_id` as text, so that ties break in the order the server breaks them. A table holding an event twice, from an SDK's retry, gives the same answer, since both rows name the same first exposure.

Your result can differ from the server's slightly: the server counts only the exposures it accepted, and moves each event's time when the SDK's clock was more than a minute off (see [Events](sdks/events.md#the-batch-and-each-event)), while your table holds what your SDKs recorded. For a stopped experiment's final check, the server also leaves out the exposures it received after the stop.
