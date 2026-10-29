import type {
  Experiment,
  ExperimentExposures,
  SampleRatioCheck,
  ExposureConflict,
  ExposureConflicts,
  ExposureTimeline,
  VariationExposures,
} from "@flagnetic/api";
import { plural } from "@lingui/core/macro";
import { Plural, Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { RefreshCwIcon, TriangleAlertIcon } from "lucide-react";
import { type ReactNode, useId, useMemo } from "react";

import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { EnvironmentName } from "@/components/protected-mark";
import { Swatch, swatchOf } from "@/components/swatch";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, type TableColumn } from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import { formatDay, formatNumber } from "@/i18n/format";
import { cn } from "@/lib/cn";
import { pValueText } from "@/lib/experiment-alerts";
import {
  exposureEnvironment,
  shareText,
  timelineDays,
  type TimelineDay,
} from "@/lib/experiment-health";
import { holdingEnvironments } from "@/lib/experiments";
import {
  environmentsQuery,
  experimentConflictsQuery,
  experimentExposuresQuery,
  experimentTimelineQuery,
  flagQuery,
} from "@/lib/queries";

export type ExperimentExposuresSectionProps = {
  project: string;
  experiment: Experiment;
  /** The environment the address names, if any. */
  environment: string | undefined;
  onEnvironment: (environment: string | undefined) => void;
};

/**
 * Whether an experiment fills up evenly, per environment: units first
 * exposed per variation against the shares its weights ask for, the same
 * per day, the exposures refused, and a sample of units seen in more than
 * one variation. It opens on the first environment whose rules serve the
 * experiment, since that is where its exposures come from.
 */
export function ExperimentExposuresSection({
  project,
  experiment,
  environment: asked,
  onEnvironment,
}: ExperimentExposuresSectionProps) {
  const { t } = useLingui();
  const environments = useQuery(environmentsQuery(project));
  // Which environments hold the experiment only picks the tab opened
  // first; without the flag the project's first environment opens.
  const flag = useQuery(flagQuery(project, experiment.flag));
  const keys = (environments.data ?? []).map(({ key }) => key);
  const holding = flag.data ? holdingEnvironments(flag.data, experiment.key) : [];
  const first = exposureEnvironment(keys, holding, undefined);
  const open = exposureEnvironment(keys, holding, asked);

  return (
    <section aria-labelledby="experiment-exposures" className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 id="experiment-exposures" className="text-lg font-semibold">
          <Trans>Exposures</Trans>
        </h2>
        <p className="max-w-prose text-sm text-muted-foreground">
          <Trans>
            Units first exposed to each variation against the shares its weights ask for, per day,
            and units exposed to more than one variation. Exposures are counted about every 10
            seconds.
          </Trans>
        </p>
      </div>
      {environments.isError ? (
        <QueryError error={environments.error} onRetry={() => void environments.refetch()} />
      ) : environments.data === undefined || (flag.isPending && asked === undefined) ? (
        <Skeleton className="h-40 w-full" />
      ) : open === undefined ? (
        <p className="text-sm text-muted-foreground">
          <Trans>The project has no environment.</Trans>
        </p>
      ) : (
        <Tabs
          label={t`Environments`}
          value={open}
          onValueChange={(next) => onEnvironment(next === first ? undefined : next)}
          tabs={environments.data.map((environment) => ({
            value: environment.key,
            label: <EnvironmentName environment={environment} />,
            content: (
              <EnvironmentExposures
                project={project}
                experiment={experiment}
                environment={environment.key}
              />
            ),
          }))}
        />
      )}
    </section>
  );
}

function EnvironmentExposures({
  project,
  experiment,
  environment,
}: {
  project: string;
  experiment: Experiment;
  environment: string;
}) {
  const summary = useQuery(experimentExposuresQuery(project, experiment.key, environment));
  const timeline = useQuery(experimentTimelineQuery(project, experiment.key, environment));
  const conflicts = useQuery(experimentConflictsQuery(project, experiment.key, environment));
  const reads = [summary, timeline, conflicts];
  const refresh = () => reads.forEach((read) => void read.refetch());

  const failed = reads.find((read) => read.isError);
  if (failed) return <QueryError error={failed.error} onRetry={refresh} />;
  if (!summary.data || !timeline.data || !conflicts.data) {
    return (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }
  return (
    <ExposuresView
      summary={summary.data}
      timeline={timeline.data}
      conflicts={conflicts.data}
      variations={experiment.variations.map(({ variation }) => variation)}
      stoppedAt={experiment.stopped_at}
      refreshing={reads.some((read) => read.isFetching)}
      onRefresh={refresh}
    />
  );
}

export type ExposuresViewProps = {
  summary: ExperimentExposures;
  timeline: ExposureTimeline;
  conflicts: ExposureConflicts;
  /** The experiment's variations in its order, which the timeline's columns follow. */
  variations: readonly string[];
  /**
   * When the experiment stopped, if it did: the only check run after is the
   * final one, which completes the check the stop took.
   */
  stoppedAt?: string | null;
  refreshing?: boolean;
  onRefresh?: () => void;
};

/** One environment's exposures, as the three reads answered them. */
export function ExposuresView({
  summary,
  timeline,
  conflicts,
  variations,
  stoppedAt = null,
  refreshing = false,
  onRefresh,
}: ExposuresViewProps) {
  const { t } = useLingui();
  const total = summary.variations.reduce((sum, row) => sum + row.first_exposures, 0);
  // The per-variation counts already list every variation, the
  // experiment's first; the timeline adds none they lack.
  const order = summary.variations.map(({ variation }) => variation);
  const days = timelineDays(timeline.items, order.length > 0 ? order : variations);
  const firstExposed = t`${plural(total, {
    one: "# unit first exposed",
    other: "# units first exposed",
  })}`;
  const conflictUnits = summary.conflicts.units;
  const conflictTotal = summary.conflicts.total;
  const expected = Object.fromEntries(
    summary.variations.map((row) => [row.variation, row.expected_share]),
  );

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          {summary.as_of ? (
            <Trans>
              {firstExposed}, counted up to <Moment value={summary.as_of} />.
            </Trans>
          ) : (
            <Trans>
              Not counted yet: the exposures are first counted within seconds of arriving.
            </Trans>
          )}
        </p>
        {onRefresh ? (
          <Button variant="outline" size="sm" disabled={refreshing} onClick={onRefresh}>
            <RefreshCwIcon aria-hidden className={cn(refreshing && "animate-spin")} />
            <Trans>Refresh</Trans>
          </Button>
        ) : null}
      </div>
      <VariationTable rows={summary.variations} />
      <Refusals rejections={summary.rejections} />
      <SampleRatioResult check={summary.sample_ratio} stoppedAt={stoppedAt} />
      <div className="flex flex-col gap-2">
        <h3 className="font-medium">
          <Trans>Per day</Trans>
        </h3>
        <TimelineTable variations={days.variations} days={days.days} expected={expected} />
      </div>
      <div className="flex flex-col gap-2">
        <h3 className="font-medium">
          <Trans>Conflicts</Trans>
        </h3>
        <p className="text-sm text-muted-foreground">
          {conflictUnits === 0 ? (
            <Trans>No unit was exposed to a variation other than its first.</Trans>
          ) : (
            t`${plural(conflictUnits, {
              one: "# unit was exposed to a variation other than its first,",
              other: "# units were exposed to a variation other than their first,",
            })} ${plural(conflictTotal, {
              one: "in # exposure.",
              other: "in # exposures.",
            })} The latest conflicts:`
          )}
        </p>
        {conflictUnits === 0 ? null : <ConflictTable items={conflicts.items} />}
      </div>
    </div>
  );
}

function VariationTable({ rows }: { rows: readonly VariationExposures[] }) {
  const { i18n, t } = useLingui();
  const columns = useMemo<TableColumn<VariationExposures>[]>(() => {
    const count = (value: number) => (
      <span className="tabular-nums">{formatNumber(i18n, value)}</span>
    );
    return [
      {
        id: "variation",
        header: t`Variation`,
        cell: ({ row }) => (
          <span className="flex items-center gap-2">
            <Swatch index={row.index} />
            <code className="font-mono text-xs">{row.original.variation}</code>
          </span>
        ),
      },
      {
        id: "first_exposures",
        header: t`Units first exposed`,
        cell: ({ row }) => count(row.original.first_exposures),
      },
      {
        id: "observed_share",
        header: t`Observed share`,
        cell: ({ row }) => (
          <span className="tabular-nums">{shareText(i18n, row.original.observed_share)}</span>
        ),
      },
      {
        id: "expected_share",
        header: t`Expected share`,
        cell: ({ row }) => (
          <span className="tabular-nums">{shareText(i18n, row.original.expected_share)}</span>
        ),
      },
      {
        id: "exposures",
        header: t`Exposures`,
        cell: ({ row }) => count(row.original.exposures),
      },
      {
        id: "overridden",
        header: t`Overridden`,
        cell: ({ row }) => count(row.original.overridden),
      },
    ];
  }, [i18n, t]);

  return (
    <Table
      caption={t`Per variation`}
      columns={columns}
      data={rows}
      getRowId={(row) => row.variation}
    />
  );
}

/**
 * The latest sample-ratio check in the environment: whether the units
 * first exposed fall to the variations as their weights ask, judged by
 * chi-square, or why the check computed nothing. An `srm` alert stays
 * active between a p-value of 0.001 and 0.01 once raised, so whether one
 * is active, not the p-value alone, says which way the check came out.
 * A stop takes a check only in the environments holding the rule at that
 * moment, so one that stopped holding it earlier keeps the last check taken
 * while the experiment ran, or none. The stop's own check is not final: the
 * exposure rollup may not have read every exposure received before the stop,
 * so the final check follows once it has, and replaces it.
 */
function SampleRatioResult({
  check,
  stoppedAt,
}: {
  check: SampleRatioCheck | null;
  stoppedAt: string | null;
}) {
  const { i18n, t } = useLingui();
  const heading = useId();
  const n = check ? formatNumber(i18n, check.n) : "";

  const stopped = stoppedAt !== null;
  const finalPending = stopped && check !== null && !check.final;
  const finalTaken =
    stopped &&
    check !== null &&
    check.final &&
    Date.parse(check.checked_at) >= Date.parse(stoppedAt);

  let verdict: ReactNode;
  if (check === null && stopped) {
    verdict = (
      <Trans>
        Not checked: the experiment stopped before a check ran in this environment, and none runs
        after a stop.
      </Trans>
    );
  } else if (check === null) {
    verdict = (
      <Trans>
        Not checked yet: a running experiment is checked every 10 minutes in each environment
        holding its rule, and once more at its stop.
      </Trans>
    );
  } else if (check.skipped_reason === "below_minimum_sample") {
    verdict = (
      <Trans>
        Skipped: the check needs 1,000 units first exposed; units first exposed so far: {n}.
      </Trans>
    );
  } else if (check.skipped_reason === "expected_below_5") {
    verdict = <Trans>Skipped: a variation expects fewer than 5 units.</Trans>;
  } else if (check.active) {
    verdict = (
      <Trans>
        Mismatch: the units first exposed are out of line with the weights, and a sample-ratio
        mismatch alert is active.
      </Trans>
    );
  } else {
    verdict = <Trans>The units first exposed match the weights.</Trans>;
  }
  const mismatch = check !== null && check.skipped_reason === null && check.active;

  return (
    <section aria-labelledby={heading} className="flex flex-col gap-2">
      <h3 id={heading} className="font-medium">
        <Trans>Sample-ratio check</Trans>
      </h3>
      <div
        className={cn(
          "flex flex-col gap-2 rounded-md border p-3 text-sm",
          mismatch ? "border-destructive/50 bg-destructive/5" : "bg-muted/40",
        )}
      >
        <p className="flex items-start gap-2">
          {mismatch ? (
            <TriangleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          ) : null}
          <span>{verdict}</span>
        </p>
        {check !== null && check.p_value !== null && check.chi_square !== null ? (
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-0.5">
            <dt className="text-muted-foreground">{t`p-value`}</dt>
            <dd className="tabular-nums">{pValueText(i18n, check.p_value)}</dd>
            <dt className="text-muted-foreground">{t`X²`}</dt>
            <dd className="tabular-nums">
              {new Intl.NumberFormat(i18n.locale, {
                minimumFractionDigits: 2,
                maximumFractionDigits: 2,
              }).format(check.chi_square)}
            </dd>
            <dt className="text-muted-foreground">{t`Units first exposed`}</dt>
            <dd className="tabular-nums">{n}</dd>
          </dl>
        ) : null}
        {check === null ? null : (
          <p className="text-muted-foreground">
            {finalPending ? (
              <Trans>
                Final check pending: this is the check the stop took on{" "}
                <Moment value={check.checked_at} />, over the exposures rolled up by then. It is
                replaced once every exposure received before the stop has been rolled up.
              </Trans>
            ) : finalTaken ? (
              <Trans>
                Final check, taken <Moment value={check.checked_at} />, over every exposure received
                before the stop; it is never run again.
              </Trans>
            ) : stopped ? (
              <Trans>
                Last checked <Moment value={check.checked_at} />, while the experiment ran; the stop
                took no check here, as the environment no longer held its rule.
              </Trans>
            ) : (
              <Trans>
                Checked <Moment value={check.checked_at} />.
              </Trans>
            )}
          </p>
        )}
      </div>
    </section>
  );
}

/** The exposures refused by reason, shown only when some were: an SDK sending what the experiment does not take. */
function Refusals({ rejections }: { rejections: ExperimentExposures["rejections"] }) {
  const { i18n, t } = useLingui();
  if (rejections.length === 0) return null;
  return (
    <div className="flex flex-col gap-1 rounded-md border bg-muted/40 p-3 text-sm">
      <p>
        <Trans>
          Some exposures were refused and are not counted above. An SDK sends exposures the
          experiment does not take:
        </Trans>
      </p>
      <ul aria-label={t`Refused exposures`} className="flex flex-col gap-0.5">
        {rejections.map(({ reason, count }) => (
          <li key={reason} className="flex gap-2">
            <code className="font-mono text-xs">{reason}</code>
            <span className="tabular-nums">{formatNumber(i18n, count)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * The day's units first exposed, split by variation into one bar, with a
 * tick where each variation's expected share ends, so a day filling
 * unevenly stands out. The bar's label reads the shares out.
 */
function DayShares({
  day,
  variations,
  expected,
}: {
  day: TimelineDay;
  variations: readonly string[];
  expected: Record<string, number>;
}) {
  const { i18n, t } = useLingui();
  if (day.firstExposures === 0) return <span className="text-muted-foreground">—</span>;
  const shares = variations.map(
    (variation) => (day.counts[variation]?.first_exposures ?? 0) / day.firstExposures,
  );
  const ticks: number[] = [];
  let sum = 0;
  for (const variation of variations.slice(0, -1)) {
    sum += expected[variation] ?? 0;
    if (sum > 0 && sum < 1) ticks.push(sum);
  }
  const label = variations
    .map((variation, index) => `${variation} ${shareText(i18n, shares[index] ?? 0)}`)
    .join(", ");

  return (
    <span
      role="img"
      aria-label={t`Shares: ${label}`}
      className="relative flex h-3 w-40 overflow-hidden rounded-sm bg-muted"
    >
      {shares.map((share, index) =>
        share > 0 ? (
          <span
            key={variations[index]}
            className={cn("h-full", swatchOf(index))}
            style={{ width: `${share * 100}%` }}
          />
        ) : null,
      )}
      {ticks.map((at) => (
        <span
          key={at}
          aria-hidden
          className="absolute inset-y-0 w-0.5 bg-foreground"
          style={{ left: `calc(${at * 100}% - 1px)` }}
        />
      ))}
    </span>
  );
}

function TimelineTable({
  variations,
  days,
  expected,
}: {
  variations: readonly string[];
  days: readonly TimelineDay[];
  expected: Record<string, number>;
}) {
  const { i18n, t } = useLingui();
  const columns = useMemo<TableColumn<TimelineDay>[]>(
    () => [
      {
        id: "day",
        header: t`Day (UTC)`,
        cell: ({ row }) => (
          <time dateTime={row.original.day} className="whitespace-nowrap">
            {formatDay(i18n, row.original.day)}
          </time>
        ),
      },
      ...variations.map<TableColumn<TimelineDay>>((variation, index) => ({
        id: `variation:${variation}`,
        header: () => (
          <span className="flex items-center gap-2">
            <Swatch index={index} />
            <code className="font-mono text-xs">{variation}</code>
          </span>
        ),
        cell: ({ row }) => {
          const counts = row.original.counts[variation];
          if (!counts) return null;
          const exposures = counts.exposures;
          const overridden = formatNumber(i18n, counts.overridden);
          return (
            <span className="flex flex-col">
              <span className="tabular-nums">{formatNumber(i18n, counts.first_exposures)}</span>
              <span className="text-xs whitespace-nowrap text-muted-foreground tabular-nums">
                {counts.overridden > 0 ? (
                  <Plural
                    value={exposures}
                    one={`# exposure, ${overridden} overridden`}
                    other={`# exposures, ${overridden} overridden`}
                  />
                ) : (
                  <Plural value={exposures} one="# exposure" other="# exposures" />
                )}
              </span>
            </span>
          );
        },
      })),
      {
        id: "shares",
        header: t`Shares against the weights`,
        cell: ({ row }) => (
          <DayShares day={row.original} variations={variations} expected={expected} />
        ),
      },
    ],
    [i18n, t, variations, expected],
  );

  return (
    <Table
      caption={t`Units first exposed per day and variation, with the exposures received that day`}
      columns={columns}
      data={days}
      stickyFirstColumn
      getRowId={(row) => row.day}
      empty={<Trans>No exposure has been received in this environment yet.</Trans>}
    />
  );
}

function ConflictTable({ items }: { items: readonly ExposureConflict[] }) {
  const { t } = useLingui();
  const columns = useMemo<TableColumn<ExposureConflict>[]>(
    () => [
      {
        id: "unit",
        header: t`Unit`,
        cell: ({ row }) => (
          <span className="flex flex-col">
            <code className="font-mono text-xs break-all">{row.original.unit_id}</code>
            <span className="text-xs text-muted-foreground">{row.original.unit_type}</span>
          </span>
        ),
      },
      {
        id: "first_variation",
        header: t`First exposed to`,
        cell: ({ row }) => (
          <code className="font-mono text-xs">{row.original.first_variation}</code>
        ),
      },
      {
        id: "conflicting_variation",
        header: t`Then exposed to`,
        cell: ({ row }) => (
          <code className="font-mono text-xs">{row.original.conflicting_variation}</code>
        ),
      },
      {
        id: "conflicting_at",
        header: t`At`,
        cell: ({ row }) => <Moment value={row.original.conflicting_at} />,
      },
    ],
    [t],
  );

  return (
    <Table
      caption={t`Conflicting units, the latest first`}
      hideCaption
      columns={columns}
      data={items}
      getRowId={(row) => `${row.unit_type}/${row.unit_id}`}
    />
  );
}
