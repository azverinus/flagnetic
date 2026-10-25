import type {
  Experiment,
  ExperimentExposures,
  ExposureConflict,
  ExposureConflicts,
  ExposureTimeline,
  VariationExposures,
} from "@flagnetic/api";
import { plural } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { RefreshCwIcon } from "lucide-react";
import { useMemo } from "react";

import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, type TableColumn } from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import { formatDay, formatNumber } from "@/i18n/format";
import { cn } from "@/lib/cn";
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

/**
 * One colour per variation, in the experiment's order, each stepped for
 * its theme so neighbours stay apart in both; a ninth variation and on
 * share the muted ink. The numbers beside every bar carry the values, so
 * no colour has to be told apart to read them. Tailwind reads class names
 * literally, so each pair is spelled out.
 */
/* eslint-disable lingui/no-unlocalized-strings -- class names, not text */
const swatchClassNames = [
  "bg-[#2a78d6] dark:bg-[#3987e5]",
  "bg-[#eb6834] dark:bg-[#d95926]",
  "bg-[#1baf7a] dark:bg-[#199e70]",
  "bg-[#eda100] dark:bg-[#c98500]",
  "bg-[#e87ba4] dark:bg-[#d55181]",
  "bg-[#008300] dark:bg-[#008300]",
  "bg-[#4a3aa7] dark:bg-[#9085e9]",
  "bg-[#e34948] dark:bg-[#e66767]",
];
/* eslint-enable lingui/no-unlocalized-strings */
const swatchOf = (index: number) => swatchClassNames[index] ?? "bg-muted-foreground";

function Swatch({ index }: { index: number }) {
  return <span aria-hidden className={cn("size-2.5 shrink-0 rounded-sm", swatchOf(index))} />;
}

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
            label: environment.name,
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
  refreshing?: boolean;
  onRefresh?: () => void;
};

/** One environment's exposures, as the three reads answered them. */
export function ExposuresView({
  summary,
  timeline,
  conflicts,
  variations,
  refreshing = false,
  onRefresh,
}: ExposuresViewProps) {
  const { i18n, t } = useLingui();
  const total = summary.variations.reduce((sum, row) => sum + row.first_exposures, 0);
  // The per-variation counts already list every variation, the
  // experiment's first; the timeline adds none they lack.
  const order = summary.variations.map(({ variation }) => variation);
  const days = timelineDays(timeline.items, order.length > 0 ? order : variations);
  const firstExposed = formatNumber(i18n, total);
  const conflictUnits = summary.conflicts.units;
  const conflictTotal = formatNumber(i18n, summary.conflicts.total);
  const expected = Object.fromEntries(
    summary.variations.map((row) => [row.variation, row.expected_share]),
  );

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          {summary.as_of ? (
            <Trans>
              {firstExposed} units first exposed, counted up to <Moment value={summary.as_of} />.
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
              one: `# unit was exposed to a variation other than its first, in ${conflictTotal} exposures. The latest conflicts:`,
              other: `# units were exposed to a variation other than their first, in ${conflictTotal} exposures. The latest conflicts:`,
            })}`
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
          const exposures = formatNumber(i18n, counts.exposures);
          const overridden = formatNumber(i18n, counts.overridden);
          return (
            <span className="flex flex-col">
              <span className="tabular-nums">{formatNumber(i18n, counts.first_exposures)}</span>
              <span className="text-xs whitespace-nowrap text-muted-foreground tabular-nums">
                {counts.overridden > 0 ? (
                  <Trans>
                    {exposures} exposures, {overridden} overridden
                  </Trans>
                ) : (
                  <Trans>{exposures} exposures</Trans>
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
