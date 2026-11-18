import type { AuditEntry } from "@flagnetic/api";
import { Card } from "@/components/ui/card";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { HistoryIcon } from "lucide-react";
import { type ComponentType, type ReactNode, useMemo } from "react";

import { type AuditFilters } from "@/components/audit-filters";
import { DownloadAudit } from "@/components/download-audit";
import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Table, type TableColumn } from "@/components/ui/table";
import { Toolbar } from "@/components/ui/toolbar";
import { actionLabel, actorKindLabel, entityKindLabel, isFiltered } from "@/lib/audit";
import { auditEntriesQuery } from "@/lib/queries";

export type AuditLogProps = {
  /** The log's filters: the address's, edited in the context panel and the toolbar. */
  filters: AuditFilters;
  /** The link to an entry's detail, keeping the search. */
  EntryLink: EntryLinkComponent;
};

/** A link to one entry's detail, around what the row shows of it. */
export type EntryLinkComponent = ComponentType<{
  entry: AuditEntry;
  className?: string;
  children: ReactNode;
}>;

/** Where an entry happened, as a column: its environment in a project, its project on the instance. */
export type Place = {
  label: string;
  /** The cell of an entry, such as the environment's name. */
  of: (entry: AuditEntry) => ReactNode;
};

export type AuditEntriesTableProps = {
  caption: string;
  entries: AuditEntry[];
  loading: boolean;
  EntryLink: EntryLinkComponent;
  place: Place;
};

/** Audit entries in the order given: when, who, what, on which entity, and where. */
export function AuditEntriesTable({
  caption,
  entries,
  loading,
  EntryLink,
  place,
}: AuditEntriesTableProps) {
  const { i18n, t } = useLingui();
  const columns = useMemo<TableColumn<AuditEntry>[]>(
    () => [
      {
        id: "time",
        header: t`Time`,
        cell: ({ row }) => <Moment value={row.original.occurred_at} full />,
      },
      {
        id: "actor",
        header: t`Actor`,
        cell: ({ row }) => <ActorCell entry={row.original} />,
      },
      {
        id: "action",
        header: t`Action`,
        cell: ({ row }) => (
          <EntryLink entry={row.original} className="flex flex-col hover:underline">
            <span className="font-medium">{actionLabel(i18n, row.original.action)}</span>
            <code className="font-mono text-xs text-muted-foreground">{row.original.action}</code>
          </EntryLink>
        ),
      },
      {
        id: "entity",
        header: t`Entity`,
        cell: ({ row }) => (
          <span className="flex flex-col">
            <span>{entityKindLabel(i18n, row.original.entity.kind)}</span>
            <code className="font-mono text-xs text-muted-foreground">
              {row.original.entity.key}
            </code>
          </span>
        ),
      },
      { id: "place", header: place.label, cell: ({ row }) => place.of(row.original) },
    ],
    [EntryLink, i18n, place, t],
  );
  return (
    <Card list>
      <Table
        caption={caption}
        hideCaption
        columns={columns}
        data={entries}
        loading={loading}
        getRowId={(entry) => entry.id}
      />
    </Card>
  );
}

/**
 * An audit log: its search and downloads in a toolbar over the entries,
 * newest first on the `Table` primitive, the other filters in the context
 * panel and echoed as chips, and keyset paging, so entries arriving while
 * the member reads never shift an older page.
 */
export function AuditLog({ filters, EntryLink }: AuditLogProps) {
  const { t } = useLingui();
  const { scope, search, apply, filter, place } = filters;
  const page = useQuery(auditEntriesQuery(scope, search));
  const filtered = isFiltered(search);

  return (
    <div className="flex flex-col gap-4">
      <Toolbar
        search={{
          value: search.q ?? "",
          placeholder: t`Action, key or actor`,
          onChange: (q) => filter({ q: q.trim() || undefined }),
        }}
        chips={filters.chips}
        onRemoveChip={filters.removeChip}
        onClearAll={filters.clearFacets}
        actions={<DownloadAudit scope={scope} search={search} />}
      />
      {page.isError ? (
        <QueryError error={page.error} onRetry={() => void page.refetch()} />
      ) : page.data?.items.length === 0 && !search.cursor ? (
        <Card list>
          <EmptyState
            icon={<HistoryIcon />}
            title={
              filtered ? (
                <Trans>No entry matches these filters.</Trans>
              ) : (
                <Trans>Nothing has been recorded yet.</Trans>
              )
            }
            action={
              filtered ? (
                <Button variant="outline" onClick={() => apply({})}>
                  <Trans>Clear filters</Trans>
                </Button>
              ) : null
            }
          />
        </Card>
      ) : (
        <>
          <AuditEntriesTable
            caption={t`Audit entries, newest first`}
            entries={page.data?.items ?? []}
            loading={page.isPending}
            EntryLink={EntryLink}
            place={place}
          />
          <nav aria-label={t`Pages of entries`} className="flex flex-wrap justify-end gap-2">
            <Button
              variant="outline"
              disabled={!search.cursor}
              onClick={() => apply({ ...search, cursor: undefined })}
            >
              <Trans>Newest entries</Trans>
            </Button>
            <Button
              variant="outline"
              disabled={!page.data?.next_cursor || page.isPlaceholderData}
              onClick={() => apply({ ...search, cursor: page.data?.next_cursor ?? undefined })}
            >
              <Trans>Older entries</Trans>
            </Button>
          </nav>
        </>
      )}
    </div>
  );
}

function ActorCell({ entry }: { entry: AuditEntry }) {
  const { i18n } = useLingui();
  return (
    <span className="flex flex-col">
      <span>{entry.actor.name}</span>
      <span className="text-xs text-muted-foreground">
        {actorKindLabel(i18n, entry.actor.kind)}
      </span>
    </span>
  );
}
