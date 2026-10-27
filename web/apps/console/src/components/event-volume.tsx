import type { EventGap } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { TriangleAlertIcon } from "lucide-react";
import { useId, useMemo } from "react";

import { Table, type TableColumn } from "@/components/ui/table";
import { formatDay, formatInstant, formatNumber } from "@/i18n/format";
import type { StackedDay } from "@/lib/daily-counts";
import { eventTypeName, eventTypes } from "@/lib/event-volume";

/** Every day of the range, the latest first, with its events per type and whether a gap touches it. */
export function VolumeTable({
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
export function Gaps({ gaps }: { gaps: readonly EventGap[] }) {
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
