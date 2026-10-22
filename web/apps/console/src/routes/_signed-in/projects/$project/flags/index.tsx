import { type FlagListItem, zFlagType } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import type { ColumnDef } from "@tanstack/react-table";
import { FlagIcon, PlusIcon } from "lucide-react";
import { type ReactNode, useId, useMemo, useState } from "react";

import { Can } from "@/components/can";
import { QueryError } from "@/components/query-error";
import { SearchField } from "@/components/search-field";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Field } from "@/components/ui/field";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/ui/page-header";
import { Select, SelectItem } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table } from "@/components/ui/table";
import {
  flagTypeName,
  flagTypes,
  type FlagsSearch,
  isFlagsFiltered,
  validateFlagsSearch,
} from "@/lib/flags";
import { madeBy } from "@/lib/project-members";
import { flagsQuery } from "@/lib/queries";
import { useKnownMembers } from "@/lib/use-known-members";

export const Route = createFileRoute("/_signed-in/projects/$project/flags/")({
  validateSearch: validateFlagsSearch,
  component: Flags,
});

/** The select's value for a filter left unset: Radix keeps the empty string for itself. */
const any = "any";

/**
 * A project's flags, filtered by what the address holds and read a page
 * at a time; creating one is offered to those who may.
 */
function Flags() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const { names } = useKnownMembers(project);

  // Filters and pages replace the address rather than adding to the
  // history, since the pages seen are remembered on the screen: going
  // back leaves the list instead of walking through its pages.
  const apply = (next: FlagsSearch) => void navigate({ search: next, replace: true });
  const filter = (patch: Partial<FlagsSearch>) => apply({ ...search, ...patch, cursor: undefined });

  const newFlag = (
    <Can permission="flag.edit" whenMissing="disable">
      <Button asChild>
        <Link to="/projects/$project/flags/-/new" params={{ project }}>
          <PlusIcon aria-hidden />
          <Trans>New flag</Trans>
        </Link>
      </Button>
    </Can>
  );

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Flags`}
        description={
          <Trans>
            Every flag exists in every environment of the project, each environment serving it by
            its own configuration.
          </Trans>
        }
        actions={newFlag}
      />
      <FlagFilters search={search} onFilter={filter} owners={names} />
      <FlagsPages
        project={project}
        search={search}
        onPage={(cursor) => apply({ ...search, cursor })}
        onClear={() => apply({})}
        newFlag={newFlag}
        owners={names}
      />
    </div>
  );
}

function FlagFilters({
  search,
  onFilter,
  owners,
}: {
  search: FlagsSearch;
  onFilter: (patch: Partial<FlagsSearch>) => void;
  owners: ReadonlyMap<string, string> | undefined;
}) {
  const { i18n, t } = useLingui();
  const archived = useId();
  const ownerOptions = useMemo(
    () =>
      [...(owners ?? new Map<string, string>())]
        .map(([value, label]) => ({ value, label }))
        .sort((a, b) => a.label.localeCompare(b.label, i18n.locale)),
    [owners, i18n.locale],
  );

  return (
    <div role="search" aria-label={t`Filter the flags`} className="flex flex-wrap items-end gap-4">
      <SearchField
        value={search.q ?? ""}
        placeholder={t`Key or name`}
        onChange={(q) => onFilter({ q: q.trim() || undefined })}
      />
      <Field label={<Trans>Type</Trans>} className="w-40">
        <Select
          value={search.type ?? any}
          onValueChange={(value) => onFilter({ type: zFlagType.safeParse(value).data })}
        >
          <SelectItem value={any}>{t`Any type`}</SelectItem>
          {flagTypes.map((type) => (
            <SelectItem key={type} value={type}>
              {flagTypeName(i18n, type)}
            </SelectItem>
          ))}
        </Select>
      </Field>
      <SearchField
        label={<Trans>Tag</Trans>}
        placeholder={t`The whole tag`}
        className="w-48"
        value={search.tag ?? ""}
        onChange={(tag) => onFilter({ tag: tag.trim() || undefined })}
      />
      <Field label={<Trans>Owner</Trans>} className="w-56">
        <Select
          value={search.owner ?? any}
          onValueChange={(value) => onFilter({ owner: value === any ? undefined : value })}
        >
          <SelectItem value={any}>{t`Any owner`}</SelectItem>
          {/* An owner the address names who is not among the members
              read: one without a grant here, once the members are read. */}
          {search.owner && owners && !owners.has(search.owner) ? (
            <SelectItem value={search.owner}>{t`A member without a grant here`}</SelectItem>
          ) : null}
          {ownerOptions.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </Select>
      </Field>
      <div className="flex h-9 items-center gap-2">
        <Switch
          id={archived}
          checked={search.archived === true}
          onCheckedChange={(checked) => onFilter({ archived: checked ? true : undefined })}
        />
        <Label htmlFor={archived}>
          <Trans>Archived flags instead</Trans>
        </Label>
      </div>
    </div>
  );
}

/**
 * The flags of one search, a page at a time. The server pages forward
 * only, by the cursor in the address; the cursors of the pages seen
 * before are kept here, so "Previous" goes back one page at a time.
 */
function FlagsPages({
  project,
  search,
  onPage,
  onClear,
  newFlag,
  owners,
}: {
  project: string;
  search: FlagsSearch;
  onPage: (cursor: string | undefined) => void;
  onClear: () => void;
  newFlag: ReactNode;
  owners: ReadonlyMap<string, string> | undefined;
}) {
  const { t } = useLingui();
  const page = useQuery(flagsQuery(project, search));
  // The cursor of every page before this one, the first page's as none;
  // an address opened on a later page has seen none, and goes back to
  // the first. The pages seen belong to one set of filters, so other
  // filters start again from their first page; they are dropped here
  // rather than by mounting the list afresh, which would blank the table
  // instead of keeping it while the new filters' page loads.
  const { cursor: _page, ...filters } = search;
  const filtersSeen = JSON.stringify(filters);
  const [pages, setPages] = useState<{ of: string; seen: (string | undefined)[] }>({
    of: filtersSeen,
    seen: [],
  });
  if (pages.of !== filtersSeen) setPages({ of: filtersSeen, seen: [] });
  const seen = pages.of === filtersSeen ? pages.seen : [];
  const setSeen = (next: (string | undefined)[]) => setPages({ of: filtersSeen, seen: next });

  if (page.isError) {
    return <QueryError error={page.error} onRetry={() => void page.refetch()} />;
  }
  if (page.data?.items.length === 0 && !search.cursor) {
    return isFlagsFiltered(search) ? (
      <EmptyState
        icon={<FlagIcon />}
        title={<Trans>No flag matches these filters.</Trans>}
        action={
          <Button variant="outline" onClick={onClear}>
            <Trans>Clear filters</Trans>
          </Button>
        }
      />
    ) : (
      <EmptyState
        icon={<FlagIcon />}
        title={<Trans>This project has no flag yet.</Trans>}
        description={<Trans>A new flag is off in every environment until it is switched on.</Trans>}
        action={newFlag}
      />
    );
  }

  const next = page.data?.next_cursor ?? undefined;
  const busy = page.isFetching;
  return (
    <>
      <FlagsTable flags={page.data?.items ?? []} loading={page.isPending} owners={owners} />
      <nav aria-label={t`Pages of flags`} className="flex flex-wrap justify-end gap-2">
        <Button
          variant="outline"
          disabled={!search.cursor || busy}
          onClick={() => {
            setSeen(seen.slice(0, -1));
            onPage(seen.at(-1));
          }}
        >
          <Trans>Previous</Trans>
        </Button>
        <Button
          variant="outline"
          disabled={next === undefined || busy}
          onClick={() => {
            setSeen([...seen, search.cursor]);
            onPage(next);
          }}
        >
          <Trans>Next</Trans>
        </Button>
      </nav>
    </>
  );
}

function FlagsTable({
  flags,
  loading,
  owners,
}: {
  flags: FlagListItem[];
  loading: boolean;
  owners: ReadonlyMap<string, string> | undefined;
}) {
  const { i18n, t } = useLingui();
  const columns = useMemo<ColumnDef<FlagListItem, unknown>[]>(
    () => [
      {
        id: "flag",
        header: t`Flag`,
        cell: ({ row }) => (
          <span className="flex flex-col">
            <code className="font-mono text-xs">{row.original.key}</code>
            <span className="text-muted-foreground">{row.original.name}</span>
          </span>
        ),
      },
      { id: "type", header: t`Type`, cell: ({ row }) => flagTypeName(i18n, row.original.type) },
      {
        id: "owner",
        header: t`Owner`,
        cell: ({ row }) => (owners ? madeBy(i18n, owners, row.original.owner) : null),
      },
      {
        id: "tags",
        header: t`Tags`,
        cell: ({ row }) =>
          row.original.tags.length > 0 ? (
            <ul aria-label={t`Tags`} className="flex flex-wrap gap-1">
              {row.original.tags.map((tag) => (
                <li key={tag} className="rounded-sm bg-muted px-1.5 py-0.5 text-xs">
                  {tag}
                </li>
              ))}
            </ul>
          ) : null,
      },
    ],
    [i18n, owners, t],
  );
  return (
    <Table
      caption={t`Flags`}
      hideCaption
      columns={columns}
      data={flags}
      loading={loading}
      getRowId={(flag) => flag.id}
    />
  );
}
