import type { EvaluationSource, Flag, FlagEvaluationDay } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { useId, useMemo } from "react";

import { DailyChart, DayRangeSelect } from "@/components/daily-chart";
import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatDay, formatNumber } from "@/i18n/format";
import { type DayRange, rangeOf } from "@/lib/daily-counts";
import {
  evaluationSeries,
  evaluationSources,
  lastEvaluatedIn,
  noVariation,
} from "@/lib/evaluations";
import { flagEvaluationsQuery } from "@/lib/queries";

export type FlagEvaluationsProps = {
  project: string;
  flag: Flag;
  environment: string;
  days: DayRange;
  onDays: (days: DayRange) => void;
};

/**
 * Whether code still reads the flag in one environment: when an SDK last
 * evaluated it, and its evaluations per UTC day over a range, what SDKs
 * reported apart from the remote evaluations the server answered. The two
 * are never added up: a client SDK is sent every flag whether its
 * application reads it or not, so only what SDKs report shows the flag is
 * read.
 */
export function FlagEvaluations({
  project,
  flag,
  environment,
  days,
  onDays,
}: FlagEvaluationsProps) {
  const { t } = useLingui();
  const heading = useId();
  // The range ends on the UTC day the section opened on.
  const range = useMemo(() => rangeOf(new Date(), days), [days]);
  const evaluations = useQuery(flagEvaluationsQuery(project, flag.key, environment, range));
  const variations = useMemo(() => flag.variations.map((variation) => variation.key), [flag]);
  const series = useMemo(
    () => evaluationSeries(evaluations.data?.items ?? [], range.days, variations),
    [evaluations.data, range, variations],
  );
  const variationName = (key: string) => (key === noVariation ? t`No variation` : key);
  const sourceText: Record<
    EvaluationSource,
    { title: string; description: string; empty: string }
  > = {
    sdk: {
      title: t`Reported by SDKs`,
      description: t`What SDKs counted evaluating in the application, per UTC day the server received their summaries.`,
      empty: t`No SDK reported evaluating the flag here in these days.`,
    },
    remote_evaluation: {
      title: t`Remote evaluations`,
      description: t`Every flag of every remote evaluation the server answered, whether the application reads the flag or not.`,
      empty: t`The server answered no remote evaluation of the flag here in these days.`,
    },
  };

  return (
    <section aria-labelledby={heading} className="flex flex-col gap-4 rounded-md border p-4">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h2 id={heading} className="text-base font-semibold">
            <Trans>Evaluations</Trans>
          </h2>
          <p className="text-sm text-muted-foreground">
            <Trans>
              Last evaluated by an SDK:{" "}
              <Moment value={lastEvaluatedIn(flag.last_evaluated, environment)} />
            </Trans>
          </p>
        </div>
        <DayRangeSelect value={days} onChange={onDays} />
      </div>
      {evaluations.isError ? (
        <QueryError error={evaluations.error} onRetry={() => void evaluations.refetch()} />
      ) : evaluations.data === undefined ? (
        <div className="flex flex-col gap-3" aria-busy>
          <Skeleton className="h-6 w-48" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : (
        <>
          {evaluationSources.map((source) => (
            <div key={source} className="flex flex-col gap-2">
              <h3 className="text-sm font-medium">{sourceText[source].title}</h3>
              <p className="text-sm text-muted-foreground">{sourceText[source].description}</p>
              <DailyChart
                label={sourceText[source].title}
                stacked={series[source]}
                nameOf={variationName}
                empty={sourceText[source].empty}
              />
            </div>
          ))}
          <details className="text-sm">
            <summary className="cursor-pointer text-muted-foreground">
              <Trans>Show as table</Trans>
            </summary>
            <EvaluationsTable
              items={evaluations.data.items}
              sourceName={(source) => sourceText[source].title}
              variationName={(variation) => variationName(variation ?? noVariation)}
            />
          </details>
        </>
      )}
    </section>
  );
}

/** Every count of the range, the latest day first, by source, variation and reason. */
function EvaluationsTable({
  items,
  sourceName,
  variationName,
}: {
  items: readonly FlagEvaluationDay[];
  sourceName: (source: EvaluationSource) => string;
  variationName: (variation: string | null) => string;
}) {
  const { i18n, t } = useLingui();
  const rows = useMemo(
    () =>
      [...items].sort(
        (a, b) =>
          (a.day < b.day ? 1 : a.day > b.day ? -1 : 0) ||
          evaluationSources.indexOf(a.source) - evaluationSources.indexOf(b.source),
      ),
    [items],
  );
  const columns = useMemo<TableColumn<FlagEvaluationDay>[]>(
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
      { id: "source", header: t`Source`, cell: ({ row }) => sourceName(row.original.source) },
      {
        id: "variation",
        header: t`Variation`,
        cell: ({ row }) =>
          row.original.variation === null ? (
            <span className="text-muted-foreground">{variationName(null)}</span>
          ) : (
            <code className="font-mono text-xs">{row.original.variation}</code>
          ),
      },
      {
        id: "reason",
        header: t`Reason`,
        cell: ({ row }) => <code className="font-mono text-xs">{row.original.reason}</code>,
      },
      {
        id: "evaluations",
        header: t`Evaluations`,
        cell: ({ row }) => (
          <span className="tabular-nums">{formatNumber(i18n, row.original.evaluations)}</span>
        ),
      },
    ],
    [i18n, sourceName, t, variationName],
  );
  return (
    <Table
      className="mt-2"
      caption={t`Evaluations per day`}
      hideCaption
      columns={columns}
      data={rows}
      empty={<Trans>No evaluation in these days.</Trans>}
      getRowId={(row) => `${row.day}/${row.source}/${row.variation ?? ""}/${row.reason}`}
    />
  );
}
