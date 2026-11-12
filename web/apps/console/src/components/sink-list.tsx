import type { Sink } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { DatabaseZapIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useMemo, useState } from "react";

import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { DeleteRowDialog, RowAction, withRowActions } from "@/components/row-dialogs";
import { SinkAtRisk, SinkHealthBadge } from "@/components/sink-status";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatLag } from "@/i18n/format";
import { kindName, lastDelivery, sinkApi, sinksKey, sinksQuery } from "@/lib/sinks";

/**
 * A project's sinks: where each delivers, whether it is enabled and how
 * it is doing, when it last delivered and how far behind it is. Creating
 * one opens its own page; a sink is deleted from its row.
 */
export function SinkList({ project }: { project: string }) {
  const { i18n, t } = useLingui();
  const navigate = useNavigate();
  const sinks = useQuery(sinksQuery(project));
  const [deleting, setDeleting] = useState<Sink>();

  const columns = useMemo<TableColumn<Sink>[]>(() => {
    const base: TableColumn<Sink>[] = [
      {
        id: "name",
        header: t`Name`,
        cell: ({ row }) => (
          <Link
            to="/projects/$project/sinks/$sink"
            params={{ project, sink: row.original.id }}
            className="font-medium hover:underline"
          >
            {row.original.name}
          </Link>
        ),
      },
      { id: "kind", header: t`Kind`, cell: ({ row }) => kindName(i18n, row.original.kind) },
      {
        id: "enabled",
        header: t`Enabled`,
        cell: ({ row }) => (row.original.enabled ? t`Yes` : t`No`),
      },
      {
        id: "health",
        header: t`Health`,
        cell: ({ row }) => <SinkHealthBadge sink={row.original} />,
      },
      {
        id: "delivery",
        header: t`Last delivery`,
        cell: ({ row }) => <Moment value={lastDelivery(row.original)} />,
      },
      {
        id: "lag",
        header: t`Lag`,
        cell: ({ row }) =>
          row.original.lag_seconds === null ? (
            <span className="text-muted-foreground">—</span>
          ) : (
            formatLag(i18n, row.original.lag_seconds)
          ),
      },
      { id: "risk", header: t`At risk`, cell: ({ row }) => <SinkAtRisk sink={row.original} /> },
    ];
    return withRowActions(base, true, (sink) => {
      const { name } = sink;
      return (
        <RowAction label={t`Delete ${name}`} icon={Trash2Icon} onClick={() => setDeleting(sink)} />
      );
    });
  }, [i18n, project, t]);

  const newSink = (
    <Button
      onClick={() => void navigate({ to: "/projects/$project/sinks/new", params: { project } })}
    >
      <PlusIcon aria-hidden />
      <Trans>New sink</Trans>
    </Button>
  );

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Sinks`}
        description={
          <Trans>
            A sink keeps a copy of the project's raw events in your own warehouse, bucket or
            pipeline, so analysts never work on the platform's database.
          </Trans>
        }
        actions={newSink}
      />
      {sinks.isError ? (
        <QueryError error={sinks.error} onRetry={() => void sinks.refetch()} />
      ) : sinks.data?.length === 0 ? (
        <EmptyState
          icon={<DatabaseZapIcon />}
          title={<Trans>No sink yet.</Trans>}
          description={
            <Trans>
              Create one to have exposures, aliases and evaluation summaries delivered to
              ClickHouse, object storage or a webhook.
            </Trans>
          }
          action={newSink}
        />
      ) : (
        <Table
          caption={t`Sinks`}
          hideCaption
          columns={columns}
          data={sinks.data ?? []}
          loading={sinks.isPending}
          getRowId={(sink) => sink.id}
        />
      )}
      <DeleteRowDialog
        rowKey={deleting?.id}
        onDone={() => setDeleting(undefined)}
        title={() => {
          const name = deleting?.name ?? "";
          return t`Delete ${name}`;
        }}
        confirmLabel={<Trans>Delete sink</Trans>}
        remove={() => (deleting ? sinkApi(project).remove(deleting) : Promise.resolve())}
        list={sinksKey(project)}
      >
        {() => (
          <p>
            <Trans>
              Nothing more is delivered to it, and its place in the event stream is forgotten. This
              cannot be undone.
            </Trans>
          </p>
        )}
      </DeleteRowDialog>
    </div>
  );
}
