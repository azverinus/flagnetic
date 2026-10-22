import { type Environment, type FlagListItem, type FlagState, zFlagType } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import type { ColumnDef } from "@tanstack/react-table";
import { FlagIcon, ListChecksIcon, PercentIcon, PlusIcon } from "lucide-react";
import { type ReactNode, useEffect, useId, useMemo, useState } from "react";

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
import { formatInstant } from "@/i18n/format";
import { cn } from "@/lib/cn";
import {
  comparedEnvironments,
  type FlagStateFilter,
  flagStateFilters,
  flagTypeName,
  flagTypes,
  type FlagsSearch,
  type FlagsSort,
  flagsSorts,
  isFlagsFiltered,
  resolveEnvironment,
  validateFlagsSearch,
} from "@/lib/flags";
import { lastEnvironment, rememberLastEnvironment } from "@/lib/preferences";
import { madeBy } from "@/lib/project-members";
import { environmentsQuery, flagsQuery } from "@/lib/queries";
import { useKnownMembers } from "@/lib/use-known-members";

export const Route = createFileRoute("/_signed-in/projects/$project/flags/")({
  validateSearch: validateFlagsSearch,
  component: Flags,
});

/** The select's value for a filter left unset: Radix keeps the empty string for itself. */
const any = "any";

function stateFilterName(i18n: I18n, state: FlagStateFilter): string {
  switch (state) {
    case "on":
      return i18n._(msg`On`);
    case "off":
      return i18n._(msg`Off`);
    case "rules":
      return i18n._(msg`Has rules`);
    case "rollout":
      return i18n._(msg`Has a rollout`);
  }
}

function sortName(i18n: I18n, sort: FlagsSort): string {
  switch (sort) {
    case "key":
      return i18n._(msg`Key`);
    case "name":
      return i18n._(msg`Name`);
    case "updated":
      return i18n._(msg`Last updated`);
  }
}

/**
 * A project's flags, filtered by what the address holds and read a page
 * at a time; creating one is offered to those who may. The address
 * always names the environment the list shows: the one asked for, else
 * the one this browser showed last in the project, else the first.
 */
function Flags() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const { names } = useKnownMembers(project);
  const environments = useQuery(environmentsQuery(project));
  const environment = environments.data
    ? resolveEnvironment(
        environments.data.map((env) => env.key),
        search.environment,
        lastEnvironment(project),
      )
    : undefined;

  useEffect(() => {
    if (environment === undefined) return;
    rememberLastEnvironment(project, environment);
    if (environment !== search.environment) {
      void navigate({ search: (prev) => ({ ...prev, environment }), replace: true });
    }
  }, [environment, navigate, project, search.environment]);

  // Filters and pages replace the address rather than adding to the
  // history, since the pages seen are remembered on the screen: going
  // back leaves the list instead of walking through its pages.
  const apply = (next: FlagsSearch) => void navigate({ search: next, replace: true });
  const filter = (patch: Partial<FlagsSearch>) => apply({ ...search, ...patch, cursor: undefined });
  const shown = { ...search, environment };

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
      {environments.isError ? (
        <QueryError error={environments.error} onRetry={() => void environments.refetch()} />
      ) : (
        <>
          <FlagFilters
            search={shown}
            onFilter={filter}
            owners={names}
            environments={environments.data ?? []}
          />
          <FlagsPages
            project={project}
            search={shown}
            ready={environment !== undefined}
            onPage={(cursor) => apply({ ...search, cursor })}
            // The environment and the order are how the flags are shown,
            // not which: clearing the filters keeps both.
            onClear={() => apply({ environment, sort: search.sort })}
            newFlag={newFlag}
            owners={names}
            environments={environments.data ?? []}
          />
        </>
      )}
    </div>
  );
}

function FlagFilters({
  search,
  onFilter,
  owners,
  environments,
}: {
  search: FlagsSearch;
  onFilter: (patch: Partial<FlagsSearch>) => void;
  owners: ReadonlyMap<string, string> | undefined;
  environments: readonly Environment[];
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
  // Every pair of environments once, in the environments' order.
  const pairs = environments.flatMap((a, i) =>
    environments.slice(i + 1).map((b) => {
      const [first, second] = [a.name, b.name];
      return { value: `${a.key},${b.key}`, label: t`${first} and ${second}` };
    }),
  );
  // The pair the address names, in whichever order, as the select offers it.
  const named = comparedEnvironments(search);
  const compared = named
    ? (pairs.find((pair) => named.every((key) => pair.value.split(",").includes(key)))?.value ??
      named.join(","))
    : undefined;

  return (
    <div role="search" aria-label={t`Filter the flags`} className="flex flex-wrap items-end gap-4">
      <Field label={<Trans>Environment</Trans>} className="w-44">
        <Select
          value={search.environment ?? ""}
          disabled={environments.length === 0}
          onValueChange={(environment) => onFilter({ environment })}
        >
          {environments.map((environment) => (
            <SelectItem key={environment.key} value={environment.key}>
              {environment.name}
            </SelectItem>
          ))}
        </Select>
      </Field>
      <Field label={<Trans>State</Trans>} className="w-40">
        <Select
          value={search.state ?? any}
          onValueChange={(value) =>
            onFilter({ state: flagStateFilters.find((state) => state === value) })
          }
        >
          <SelectItem value={any}>{t`Any state`}</SelectItem>
          {flagStateFilters.map((state) => (
            <SelectItem key={state} value={state}>
              {stateFilterName(i18n, state)}
            </SelectItem>
          ))}
        </Select>
      </Field>
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
      <Field
        label={<Trans>Differs between</Trans>}
        hint={<Trans>In what SDKs receive.</Trans>}
        className="w-64"
      >
        <Select
          value={compared ?? any}
          disabled={pairs.length === 0}
          onValueChange={(value) =>
            onFilter({ differs_between: value === any ? undefined : value })
          }
        >
          <SelectItem value={any}>{t`Any environments`}</SelectItem>
          {pairs.map((pair) => (
            <SelectItem key={pair.value} value={pair.value}>
              {pair.label}
            </SelectItem>
          ))}
        </Select>
      </Field>
      <Field label={<Trans>Sort by</Trans>} className="w-40">
        <Select
          value={search.sort ?? "key"}
          onValueChange={(value) => {
            const sort = flagsSorts.find((candidate) => candidate === value);
            onFilter({ sort: sort === "key" ? undefined : sort });
          }}
        >
          {flagsSorts.map((sort) => (
            <SelectItem key={sort} value={sort}>
              {sortName(i18n, sort)}
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
  ready,
  onPage,
  onClear,
  newFlag,
  owners,
  environments,
}: {
  project: string;
  search: FlagsSearch;
  /** Whether the environment the list shows is known, which a state filter is judged in. */
  ready: boolean;
  onPage: (cursor: string | undefined) => void;
  onClear: () => void;
  newFlag: ReactNode;
  owners: ReadonlyMap<string, string> | undefined;
  environments: readonly Environment[];
}) {
  const { t } = useLingui();
  const page = useQuery({ ...flagsQuery(project, search), enabled: ready });
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
      <FlagsTable
        project={project}
        environment={search.environment}
        flags={page.data?.items ?? []}
        loading={page.isPending}
        owners={owners}
        environments={environments}
      />
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

/** What a flag serves in one environment at a glance: on or off, rules, a rollout. */
function StateCell({ state }: { state: FlagState | undefined }) {
  const { t } = useLingui();
  if (!state) return <span className="text-muted-foreground">—</span>;
  return (
    <span className="flex items-center gap-1.5">
      <span
        className={cn(
          "rounded-sm px-1.5 py-0.5 text-xs font-medium",
          state.on ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground",
        )}
      >
        {state.on ? <Trans>On</Trans> : <Trans>Off</Trans>}
      </span>
      {state.has_rules ? (
        <span title={t`Has rules`} className="text-muted-foreground">
          <ListChecksIcon aria-hidden className="size-4" />
          <span className="sr-only">{t`Has rules`}</span>
        </span>
      ) : null}
      {state.has_rollout ? (
        <span title={t`Has a rollout`} className="text-muted-foreground">
          <PercentIcon aria-hidden className="size-4" />
          <span className="sr-only">{t`Has a rollout`}</span>
        </span>
      ) : null}
    </span>
  );
}

function FlagsTable({
  project,
  environment,
  flags,
  loading,
  owners,
  environments,
}: {
  project: string;
  /** The environment the list shows, whose tab a flag opens in. */
  environment: string | undefined;
  flags: FlagListItem[];
  loading: boolean;
  owners: ReadonlyMap<string, string> | undefined;
  environments: readonly Environment[];
}) {
  const { i18n, t } = useLingui();
  const columns = useMemo<ColumnDef<FlagListItem, unknown>[]>(
    () => [
      {
        id: "flag",
        header: t`Flag`,
        cell: ({ row }) => (
          <Link
            to="/projects/$project/flags/$flag"
            params={{ project, flag: row.original.key }}
            search={{ environment }}
            className="flex flex-col hover:underline"
          >
            <code className="font-mono text-xs">{row.original.key}</code>
            <span className="text-muted-foreground">{row.original.name}</span>
          </Link>
        ),
      },
      { id: "type", header: t`Type`, cell: ({ row }) => flagTypeName(i18n, row.original.type) },
      ...environments.map((env): ColumnDef<FlagListItem, unknown> => ({
        id: `state-${env.key}`,
        header: env.name,
        cell: ({ row }) => (
          <StateCell state={row.original.states.find((state) => state.environment === env.key)} />
        ),
      })),
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
      {
        id: "updated",
        header: t`Updated`,
        cell: ({ row }) => (
          <time
            dateTime={row.original.updated_at}
            title={row.original.updated_at}
            className="whitespace-nowrap"
          >
            {formatInstant(i18n, row.original.updated_at)}
          </time>
        ),
      },
    ],
    [environment, environments, i18n, owners, project, t],
  );
  return (
    <Table
      caption={t`Flags`}
      hideCaption
      stickyFirstColumn
      columns={columns}
      data={flags}
      loading={loading}
      getRowId={(flag) => flag.id}
    />
  );
}
