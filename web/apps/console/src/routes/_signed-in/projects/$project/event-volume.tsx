import type { EventGap, EventType } from "@flagnetic/api";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { TriangleAlertIcon } from "lucide-react";
import { useId, useMemo } from "react";

import { DailyChart, DayRangeSelect } from "@/components/daily-chart";
import { QueryError } from "@/components/query-error";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatDay, formatInstant, formatNumber } from "@/i18n/format";
import {
  dayRangeOf,
  daysSearchOf,
  rangeOf,
  type StackedDay,
  validateDaysSearch,
} from "@/lib/daily-counts";
import { eventTypeName, eventTypes, gapDays, volumeSeries } from "@/lib/event-volume";
import { eventVolumeQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/projects/$project/event-volume")({
  validateSearch: validateDaysSearch,
  staticData: { crumb: msg`Event volume` },
  component: EventVolume,
});

/**
 * What the project's event store received per UTC day and type of event,
 * and what retention dropped before the rollups read it: a gap, whose
 * events are missing from every count read from them.
 */
function EventVolume() {
  const { i18n, t } = useLingui();
  const { project } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const days = dayRangeOf(search);
  // The range ends on the UTC day the page opened on.
  const range = useMemo(() => rangeOf(new Date(), days), [days]);
  const volume = useQuery(eventVolumeQuery(project, range));
  const series = useMemo(
    () => volumeSeries(volume.data?.days ?? [], range.days),
    [volume.data, range],
  );
  const touched = useMemo(() => gapDays(volume.data?.gaps ?? [], range.days), [volume.data, range]);
  const typeName = (key: string) => eventTypeName(i18n, key as EventType);

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Event volume`}
        description={
          <Trans>
            The events SDKs posted to the project per UTC day they were received, as the rollups
            counted them. The counts stay after retention drops the events themselves.
          </Trans>
        }
        actions={
          <DayRangeSelect
            value={days}
            onChange={(next) =>
              void navigate({ search: { days: daysSearchOf(next) }, replace: true })
            }
          />
        }
      />
      {volume.isError ? (
        <QueryError error={volume.error} onRetry={() => void volume.refetch()} />
      ) : volume.data === undefined ? (
        <div className="flex flex-col gap-3" aria-busy>
          <Skeleton className="h-6 w-48" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : (
        <>
          <DailyChart
            label={t`Events per day`}
            stacked={series}
            nameOf={typeName}
            marked={{ days: touched, name: t`Days with a gap` }}
            empty={<Trans>The project received no event in these days.</Trans>}
          />
          <details className="text-sm">
            <summary className="cursor-pointer text-muted-foreground">
              <Trans>Show as table</Trans>
            </summary>
            <VolumeTable days={series.days} touched={touched} />
          </details>
          <Gaps gaps={volume.data.gaps} />
        </>
      )}
    </div>
  );
}

/** Every day of the range, the latest first, with its events per type and whether a gap touches it. */
function VolumeTable({
  days,
  touched,
}: {
  days: readonly StackedDay[];
  touched: ReadonlySet<string>;
}) {
  const { i18n, t } = useLingui();
  const rows = useMemo(() => [...days].reverse(), [days]);
  const columns = useMemo<TableColumn<StackedDay>[]>(
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
      ...eventTypes.map((type): TableColumn<StackedDay> => ({
        id: type,
        header: eventTypeName(i18n, type),
        cell: ({ row }) => (
          <span className="tabular-nums">{formatNumber(i18n, row.original.counts[type] ?? 0)}</span>
        ),
      })),
      {
        id: "total",
        header: t`Total`,
        cell: ({ row }) => (
          <span className="tabular-nums">{formatNumber(i18n, row.original.total)}</span>
        ),
      },
      {
        id: "gap",
        header: t`Gap`,
        cell: ({ row }) =>
          touched.has(row.original.day) ? (
            <span className="flex items-center gap-1 text-destructive">
              <TriangleAlertIcon aria-hidden className="size-3.5" />
              <Trans>Events lost</Trans>
            </span>
          ) : null,
      },
    ],
    [i18n, t, touched],
  );
  return (
    <Table
      className="mt-2"
      caption={t`Events per day`}
      hideCaption
      columns={columns}
      data={rows}
      getRowId={(day) => day.day}
    />
  );
}

/**
 * The events retention dropped before a rollup had read them, whose
 * received period meets the range. Gaps are the instance's, since a
 * rollup reads every project's events, so their rows count every
 * project's.
 */
function Gaps({ gaps }: { gaps: readonly EventGap[] }) {
  const { i18n, t } = useLingui();
  const heading = useId();
  const columns = useMemo<TableColumn<EventGap>[]>(
    () => [
      {
        id: "type",
        header: t`Events`,
        cell: ({ row }) => (
          <span className="flex flex-col">
            {eventTypeName(i18n, row.original.type)}
            <code className="font-mono text-xs text-muted-foreground">{row.original.stream}</code>
          </span>
        ),
      },
      {
        id: "period",
        header: t`Received`,
        cell: ({ row }) => {
          const from = formatInstant(i18n, row.original.period_start);
          const to = formatInstant(i18n, row.original.period_end);
          return (
            <span className="whitespace-nowrap">
              <Trans>
                {from} to {to}
              </Trans>
            </span>
          );
        },
      },
      {
        id: "rows",
        header: t`Events lost`,
        cell: ({ row }) => (
          <span className="tabular-nums">{formatNumber(i18n, row.original.rows)}</span>
        ),
      },
      {
        id: "dropped_at",
        header: t`Dropped`,
        cell: ({ row }) => (
          <time dateTime={row.original.dropped_at} className="whitespace-nowrap">
            {formatInstant(i18n, row.original.dropped_at)}
          </time>
        ),
      },
    ],
    [i18n, t],
  );
  return (
    <section aria-labelledby={heading} className="flex flex-col gap-2">
      <h2 id={heading} className="text-base font-semibold">
        <Trans>Gaps</Trans>
      </h2>
      <p className="text-sm text-muted-foreground">
        <Trans>
          Events retention dropped before a rollup had read them: they are missing from the counts
          above and from everything read from them, such as evaluations and exposures. A rollup
          reads every project's events, so a gap counts the events of every project of the instance.
        </Trans>
      </p>
      <Table
        caption={t`Gaps`}
        hideCaption
        columns={columns}
        data={gaps}
        empty={<Trans>No event was lost in these days.</Trans>}
        getRowId={(gap) => `${gap.stream}/${gap.period_start}/${gap.dropped_at}`}
      />
    </section>
  );
}
