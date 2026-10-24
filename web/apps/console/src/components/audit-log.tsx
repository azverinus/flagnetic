import type { AuditEntry } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { HistoryIcon } from "lucide-react";
import { type ComponentType, type FormEvent, type ReactNode, useMemo, useState } from "react";

import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatInstant } from "@/i18n/format";
import {
  actionLabel,
  actorKindLabel,
  actorOptions,
  auditActions,
  auditActorKinds,
  type AuditSearch,
  auditEntityKinds,
  entityKindLabel,
  type FilterOption,
  isFiltered,
} from "@/lib/audit";
import { type AuditScope, auditEntriesQuery } from "@/lib/queries";

/** The select's value for a filter left unset: Radix keeps the empty string for itself. */
const any = "any";

export type AuditLogProps = {
  scope: AuditScope;
  search: AuditSearch;
  /** Replaces the search, the page included. */
  onSearch: (search: AuditSearch) => void;
  /** The link to an entry's detail, keeping the search. */
  EntryLink: EntryLinkComponent;
  /**
   * The members the screen knows of to filter by; whoever else acted in
   * the entries shown is offered too.
   */
  actors: FilterOption[];
  /** The scope's own filter and column: the environment in a project, the project on the instance. */
  place: Place & { filter: "environment" | "project"; options: FilterOption[] };
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
        cell: ({ row }) => (
          <time
            dateTime={row.original.occurred_at}
            title={row.original.occurred_at}
            className="whitespace-nowrap"
          >
            {formatInstant(i18n, row.original.occurred_at)}
          </time>
        ),
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
    <Table
      caption={caption}
      hideCaption
      columns={columns}
      data={entries}
      loading={loading}
      getRowId={(entry) => entry.id}
    />
  );
}

/**
 * An audit log: filters as search parameters, entries newest first on the
 * `Table` primitive, and keyset paging, so entries arriving while the
 * member reads never shift an older page.
 */
export function AuditLog({ scope, search, onSearch, EntryLink, actors, place }: AuditLogProps) {
  const { i18n, t } = useLingui();
  const page = useQuery(auditEntriesQuery(scope, search));
  const filtered = isFiltered(search);
  const actorChoices = useMemo(
    () => actorOptions(i18n, actors, page.data?.items ?? [], search.actor),
    [actors, i18n, page.data, search.actor],
  );

  return (
    <div className="flex flex-col gap-6">
      <AuditFilters
        key={JSON.stringify({ ...search, cursor: undefined })}
        search={search}
        onSearch={onSearch}
        actors={actorChoices}
        place={place}
      />
      {page.isError ? (
        <QueryError error={page.error} onRetry={() => void page.refetch()} />
      ) : page.data?.items.length === 0 && !search.cursor ? (
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
              <Button variant="outline" onClick={() => onSearch({})}>
                <Trans>Clear filters</Trans>
              </Button>
            ) : null
          }
        />
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
              onClick={() => onSearch({ ...search, cursor: undefined })}
            >
              <Trans>Newest entries</Trans>
            </Button>
            <Button
              variant="outline"
              disabled={!page.data?.next_cursor || page.isPlaceholderData}
              onClick={() => onSearch({ ...search, cursor: page.data?.next_cursor ?? undefined })}
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

/**
 * The filters, edited here and applied together, so the address and the
 * request change once per search rather than on every keystroke.
 */
function AuditFilters({
  search,
  onSearch,
  actors,
  place,
}: Pick<AuditLogProps, "search" | "onSearch" | "actors" | "place">) {
  const { i18n, t } = useLingui();
  const [draft, setDraft] = useState<AuditSearch>(search);
  const set = (name: keyof AuditSearch, value: string) =>
    setDraft((current) => ({
      ...current,
      [name]: value === "" || value === any ? undefined : value,
    }));
  const apply = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    onSearch({ ...draft, q: draft.q?.trim() || undefined, cursor: undefined });
  };

  const choice = (name: keyof AuditSearch, label: string, options: FilterOption[]): ReactNode => (
    <Field label={label}>
      <Select value={draft[name] ?? any} onValueChange={(value) => set(name, value)}>
        <SelectItem value={any}>{t`Any`}</SelectItem>
        {options.map((option) => (
          <SelectItem key={option.value} value={option.value}>
            {option.label}
          </SelectItem>
        ))}
      </Select>
    </Field>
  );

  return (
    <form
      role="search"
      aria-label={t`Filter the audit log`}
      onSubmit={apply}
      className="flex flex-col gap-4 rounded-lg border p-4"
    >
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Field
          label={<Trans>Search</Trans>}
          className="sm:col-span-2"
          hint={<Trans>In the action, the entity's key and the actor's name.</Trans>}
        >
          <Input
            type="search"
            value={draft.q ?? ""}
            onChange={(event) => set("q", event.target.value)}
          />
        </Field>
        {choice(
          "action",
          t`Action`,
          auditActions.map((action) => ({ value: action, label: actionLabel(i18n, action) })),
        )}
        {choice(
          "entity_kind",
          t`Entity`,
          auditEntityKinds.map((kind) => ({ value: kind, label: entityKindLabel(i18n, kind) })),
        )}
        {choice("actor", t`Member`, actors)}
        {choice(
          "actor_kind",
          t`Actor kind`,
          auditActorKinds.map((kind) => ({ value: kind, label: actorKindLabel(i18n, kind) })),
        )}
        {choice(place.filter, place.label, place.options)}
        <div className="grid grid-cols-2 gap-4">
          <Field label={<Trans>From</Trans>}>
            <Input
              type="date"
              value={draft.from ?? ""}
              onChange={(event) => set("from", event.target.value)}
            />
          </Field>
          <Field label={<Trans>To</Trans>}>
            <Input
              type="date"
              value={draft.to ?? ""}
              onChange={(event) => set("to", event.target.value)}
            />
          </Field>
        </div>
      </div>
      <div className="flex flex-wrap justify-end gap-2">
        {isFiltered(search) ? (
          <Button type="button" variant="outline" onClick={() => onSearch({})}>
            <Trans>Clear filters</Trans>
          </Button>
        ) : null}
        <Button type="submit">
          <Trans>Apply filters</Trans>
        </Button>
      </div>
    </form>
  );
}
