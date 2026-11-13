import type { Sink, SinkStream } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMemo } from "react";

import { Moment } from "@/components/moment";
import { Table, type TableColumn } from "@/components/ui/table";
import { eventKindName, failureText } from "@/lib/sinks";

/** Each event kind's delivery: when it last delivered and how it last failed. */
export function SinkStreams({ sink }: { sink: Sink }) {
  const { i18n, t } = useLingui();
  const columns = useMemo<TableColumn<SinkStream>[]>(
    () => [
      {
        id: "kind",
        header: t`Event kind`,
        cell: ({ row }) => eventKindName(i18n, row.original.event_kind),
      },
      {
        id: "delivered",
        header: t`Last delivery`,
        cell: ({ row }) => <Moment value={row.original.last_delivered_at} />,
      },
      {
        id: "failure",
        header: t`Last failure`,
        cell: ({ row }) => {
          const failure = row.original.last_failure;
          return failure ? (
            <span className="flex flex-col">
              <span>{failureText(i18n, failure)}</span>
              <span className="text-muted-foreground">
                <Moment value={failure.at} />
              </span>
            </span>
          ) : (
            <span className="text-muted-foreground">—</span>
          );
        },
      },
    ],
    [i18n, t],
  );
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-lg font-semibold">
        <Trans>Streams</Trans>
      </h2>
      {sink.streams.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          <Trans>Each event kind gets a stream once the sink is enabled.</Trans>
        </p>
      ) : (
        <Table
          caption={t`Streams`}
          hideCaption
          columns={columns}
          data={sink.streams}
          getRowId={(stream) => stream.event_kind}
        />
      )}
    </section>
  );
}
