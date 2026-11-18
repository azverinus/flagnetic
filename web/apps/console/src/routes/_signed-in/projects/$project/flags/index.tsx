import { type Environment, type FlagListItem, type FlagState } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { FlagIcon, PlusIcon } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useState } from "react";

import { Can } from "@/components/can";
import { FlagsPanel, useFlagsFilters } from "@/components/flags-panel";
import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { Lamp } from "@/components/ui/lamp";
import { Page } from "@/components/ui/page";
import { PageHeader } from "@/components/ui/page-header";
import { Table, type TableColumn } from "@/components/ui/table";
import { Toolbar } from "@/components/ui/toolbar";
import { environmentLabel } from "@/lib/environments";
import {
  flagTypeName,
  type FlagsSearch,
  type FlagsSort,
  flagsSorts,
  isFlagsFiltered,
  validateFlagsSearch,
} from "@/lib/flags";
import { rememberLastEnvironment } from "@/lib/preferences";
import { madeBy } from "@/lib/project-members";
import { flagsQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/projects/$project/flags/")({
  validateSearch: validateFlagsSearch,
  staticData: { panel: FlagsPanel },
  component: Flags,
});

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
 * A project's flags, narrowed by the facets in the context panel and read
 * a page at a time; creating one is offered to those who may. The address
 * always names the environment the list shows: the one asked for, else
 * the one this browser showed last in the project, else the first.
 */
function Flags() {
  const { i18n, t } = useLingui();
  const { project } = Route.useParams();
  const asked = Route.useSearch();
  const navigate = Route.useNavigate();
  const {
    search,
    environmentsRead,
    environments,
    owners,
    apply,
    filter,
    chips,
    removeChip,
    clearFacets,
  } = useFlagsFilters();
  const environment = search.environment;
  const environmentsKnown = environments.length > 0;

  useEffect(() => {
    if (environment === undefined || !environmentsKnown) return;
    rememberLastEnvironment(project, environment);
    if (environment !== asked.environment) {
      void navigate({ search: (prev) => ({ ...prev, environment }), replace: true });
    }
  }, [environment, environmentsKnown, navigate, project, asked.environment]);

  // The header's create action shows once the project is known to have
  // flags: while the list loads, fails or is empty it would flicker away,
  // and the empty state carries it. A filtered list or a later page implies
  // flags exist, so filtering keeps it in place.
  const list = useQuery({ ...flagsQuery(project, search), enabled: environmentsKnown });
  const known =
    Boolean(search.cursor) || isFlagsFiltered(search) || (list.data?.items.length ?? 0) > 0;

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
    <Page>
      <PageHeader title={t`Flags`} actions={known ? newFlag : null} />
      {environmentsRead.isError ? (
        <QueryError
          error={environmentsRead.error}
          onRetry={() => void environmentsRead.refetch()}
        />
      ) : (
        <>
          <Toolbar
            search={{
              value: search.q ?? "",
              placeholder: t`Key or name`,
              onChange: (q) => filter({ q: q.trim() || undefined }),
            }}
            sort={{
              value: search.sort ?? "key",
              options: flagsSorts.map((sort) => ({ value: sort, label: sortName(i18n, sort) })),
              onChange: (value) => {
                const sort = flagsSorts.find((candidate) => candidate === value);
                filter({ sort: sort === "key" ? undefined : sort });
              },
            }}
            chips={chips}
            onRemoveChip={removeChip}
            onClearAll={clearFacets}
          />
          <FlagsPages
            project={project}
            search={search}
            ready={environmentsKnown}
            onPage={(cursor) => apply({ ...search, cursor })}
            // Clearing the filters from the empty list keeps the view, as
            // "Clear all" does: the archived flags stay the archived ones.
            onClear={clearFacets}
            newFlag={newFlag}
            owners={owners}
            environments={environments}
          />
        </>
      )}
    </Page>
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
    // The archived flags are a view, not a filter: with nothing else set,
    // an empty one says there are none rather than offering to clear it.
    if (search.archived && !isFlagsFiltered({ ...search, archived: undefined })) {
      return (
        <Card list>
          <EmptyState icon={<FlagIcon />} title={<Trans>No flag is archived.</Trans>} />
        </Card>
      );
    }
    return isFlagsFiltered(search) ? (
      <Card list>
        <EmptyState
          icon={<FlagIcon />}
          title={<Trans>No flag matches these filters.</Trans>}
          action={
            <Button variant="outline" onClick={onClear}>
              <Trans>Clear filters</Trans>
            </Button>
          }
        />
      </Card>
    ) : (
      <Card list>
        <EmptyState
          icon={<FlagIcon />}
          title={<Trans>This project has no flag yet.</Trans>}
          description={
            <Trans>A new flag is off in every environment until it is switched on.</Trans>
          }
          action={newFlag}
        />
      </Card>
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

/**
 * What a flag serves in one environment at a glance: the state lamp, then
 * in muted words what the list's answer carries of how: rules, a rollout,
 * an experiment.
 */
function StateCell({ state }: { state: FlagState | undefined }) {
  const { t } = useLingui();
  if (!state) return <span className="text-muted-foreground">—</span>;
  const how = [
    state.has_rules ? t`Rules` : null,
    state.has_rollout ? t`Rollout` : null,
    state.has_experiment ? t`Experiment` : null,
  ].filter((word) => word !== null);
  return (
    <span className="flex flex-col gap-0.5">
      <Lamp on={state.on} />
      {how.length > 0 ? (
        <span className="text-xs text-muted-foreground">{how.join(" · ")}</span>
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
  const columns = useMemo<TableColumn<FlagListItem>[]>(
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
      ...environments.map((env): TableColumn<FlagListItem> => ({
        id: `state-${env.key}`,
        header: environmentLabel(i18n, env),
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
        cell: ({ row }) => <Moment value={row.original.updated_at} relative />,
      },
    ],
    [environment, environments, i18n, owners, project, t],
  );
  return (
    <Card list>
      <Table
        caption={t`Flags`}
        hideCaption
        stickyFirstColumn
        columns={columns}
        data={flags}
        loading={loading}
        getRowId={(flag) => flag.id}
      />
    </Card>
  );
}
