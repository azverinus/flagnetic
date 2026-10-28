import type { StaleFlag, StaleReason } from "@flagnetic/api";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { HourglassIcon } from "lucide-react";
import { useMemo, useState } from "react";

import { Moment } from "@/components/moment";
import { QueryError } from "@/components/query-error";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatDateTime, formatDay } from "@/i18n/format";
import { environmentLabel } from "@/lib/environments";
import { madeBy } from "@/lib/project-members";
import { environmentsQuery, staleFlagsQuery } from "@/lib/queries";
import {
  staleReasonDescription,
  staleReasonName,
  staleReasons,
  type StaleFlagsSearch,
  toggleReason,
  validateStaleFlagsSearch,
} from "@/lib/stale-flags";
import { useKnownMembers } from "@/lib/use-known-members";

export const Route = createFileRoute("/_signed-in/projects/$project/stale-flags")({
  validateSearch: validateStaleFlagsSearch,
  staticData: { crumb: msg`Stale flags` },
  component: StaleFlags,
});

/**
 * The project's stale flags with why each is stale, to work through: each
 * links to its flag, where it is cleaned up or archived. Filtered by the
 * reasons the address holds, read a page at a time.
 */
function StaleFlags() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();

  // Filters and pages replace the address rather than adding to the
  // history, since the pages seen are remembered on the screen: going
  // back leaves the list instead of walking through its pages.
  const apply = (next: StaleFlagsSearch) => void navigate({ search: next, replace: true });

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Stale flags`}
        description={
          <Trans>
            Flags that may be ready to leave the code: each one with why it is stale. Archived flags
            are never listed.
          </Trans>
        }
      />
      <ReasonFilter
        reasons={search.reason}
        onToggle={(reason) => apply({ reason: toggleReason(search.reason, reason) })}
      />
      <StaleFlagsPages
        project={project}
        search={search}
        onPage={(cursor) => apply({ ...search, cursor })}
        onClear={() => apply({})}
      />
    </div>
  );
}

/** One toggle per reason: a flag is listed when it is stale for any reason pressed, every flag when none is. */
function ReasonFilter({
  reasons,
  onToggle,
}: {
  reasons: readonly StaleReason[] | undefined;
  onToggle: (reason: StaleReason) => void;
}) {
  const { i18n, t } = useLingui();
  return (
    <div role="group" aria-label={t`Stale for`} className="flex flex-col gap-2">
      <p className="text-sm text-muted-foreground">
        <Trans>
          Show flags stale for any of these reasons; none pressed shows every stale flag.
        </Trans>
      </p>
      <ul className="flex flex-wrap gap-2">
        {staleReasons.map((reason) => {
          const pressed = reasons?.includes(reason) ?? false;
          return (
            <li key={reason}>
              <Button
                variant={pressed ? "default" : "outline"}
                size="sm"
                aria-pressed={pressed}
                title={staleReasonDescription(i18n, reason)}
                onClick={() => onToggle(reason)}
              >
                {staleReasonName(i18n, reason)}
              </Button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

/**
 * The stale flags of one search, a page at a time. The server pages
 * forward only, by the cursor in the address; the cursors of the pages
 * seen before are kept here, so "Previous" goes back one page at a time.
 */
function StaleFlagsPages({
  project,
  search,
  onPage,
  onClear,
}: {
  project: string;
  search: StaleFlagsSearch;
  onPage: (cursor: string | undefined) => void;
  onClear: () => void;
}) {
  const { t } = useLingui();
  const page = useQuery(staleFlagsQuery(project, search));
  // The cursor of every page before this one, the first page's as none;
  // other filters start again from their first page.
  const filtersSeen = JSON.stringify(search.reason ?? []);
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
    return search.reason ? (
      <EmptyState
        icon={<HourglassIcon />}
        title={<Trans>No flag is stale for these reasons.</Trans>}
        action={
          <Button variant="outline" onClick={onClear}>
            <Trans>Clear filters</Trans>
          </Button>
        }
      />
    ) : (
      <EmptyState
        icon={<HourglassIcon />}
        title={<Trans>No flag is stale.</Trans>}
        description={
          <Trans>
            A flag shows up here once it outlives its expected removal, stops being evaluated, loses
            its owner, finishes its rollout or keeps an overdue experiment.
          </Trans>
        }
      />
    );
  }

  const next = page.data?.next_cursor ?? undefined;
  const busy = page.isFetching;
  return (
    <>
      <StaleFlagsTable project={project} flags={page.data?.items ?? []} loading={page.isPending} />
      <nav aria-label={t`Pages of stale flags`} className="flex flex-wrap justify-end gap-2">
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

/** Why a flag is stale, each reason explained on hover; an overdue experiment named with its expected end. */
function Reasons({ project, flag }: { project: string; flag: StaleFlag }) {
  const { i18n, t } = useLingui();
  return (
    <ul aria-label={t`Reasons`} className="flex flex-col items-start gap-1">
      {flag.reasons.map((reason) => (
        <li key={reason} className="flex flex-wrap items-center gap-1">
          <Badge variant="muted" title={staleReasonDescription(i18n, reason)}>
            {staleReasonName(i18n, reason)}
          </Badge>
          {reason === "experiment_overdue" && flag.experiment ? (
            <OverdueExperiment project={project} experiment={flag.experiment} />
          ) : null}
        </li>
      ))}
    </ul>
  );
}

function OverdueExperiment({
  project,
  experiment,
}: {
  project: string;
  experiment: NonNullable<StaleFlag["experiment"]>;
}) {
  const { i18n } = useLingui();
  const end = formatDay(i18n, experiment.expected_end_at);
  const link = (
    <Link
      to="/projects/$project/experiments/$experiment"
      params={{ project, experiment: experiment.key }}
      className="font-mono text-xs hover:underline"
    >
      {experiment.key}
    </Link>
  );
  return (
    <span className="text-xs text-muted-foreground">
      <Trans>
        {link}, expected to end {end}
      </Trans>
    </span>
  );
}

function StaleFlagsTable({
  project,
  flags,
  loading,
}: {
  project: string;
  flags: StaleFlag[];
  loading: boolean;
}) {
  const { i18n, t } = useLingui();
  const { names } = useKnownMembers(project);
  const environments = useQuery(environmentsQuery(project));
  const nameOf = useMemo(
    () =>
      new Map(
        environments.data?.map((environment) => [
          environment.key,
          environmentLabel(i18n, environment),
        ]),
      ),
    [environments.data, i18n],
  );
  const columns = useMemo<TableColumn<StaleFlag>[]>(
    () => [
      {
        id: "flag",
        header: t`Flag`,
        cell: ({ row }) => (
          <Link
            to="/projects/$project/flags/$flag"
            params={{ project, flag: row.original.key }}
            className="flex flex-col hover:underline"
          >
            <code className="font-mono text-xs">{row.original.key}</code>
            <span className="text-muted-foreground">{row.original.name}</span>
          </Link>
        ),
      },
      {
        id: "reasons",
        header: t`Stale for`,
        cell: ({ row }) => <Reasons project={project} flag={row.original} />,
      },
      {
        id: "owner",
        header: t`Owner`,
        cell: ({ row }) => (names ? madeBy(i18n, names, row.original.owner) : null),
      },
      {
        id: "lifetime",
        header: t`Lifetime`,
        cell: ({ row }) => {
          if (row.original.lifetime === "permanent") return <Trans>Permanent</Trans>;
          const removal = row.original.expected_removal_at;
          if (removal === null) return <Trans>Temporary</Trans>;
          const by = formatDateTime(i18n, removal);
          return (
            <span className="flex flex-col">
              <Trans>Temporary</Trans>
              <span className="text-xs text-muted-foreground">
                <Trans>to be removed by {by}</Trans>
              </span>
            </span>
          );
        },
      },
      {
        id: "last_evaluated",
        header: t`Last evaluated`,
        cell: ({ row }) => (
          <dl className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-xs">
            {row.original.last_evaluated.map((entry) => (
              <div key={entry.environment} className="contents">
                <dt className="text-muted-foreground">
                  {nameOf.get(entry.environment) ?? entry.environment}
                </dt>
                <dd>
                  <Moment value={entry.last_evaluated_at} />
                </dd>
              </div>
            ))}
          </dl>
        ),
      },
    ],
    [i18n, names, nameOf, project, t],
  );
  return (
    <Table
      caption={t`Stale flags`}
      hideCaption
      stickyFirstColumn
      columns={columns}
      data={flags}
      loading={loading}
      getRowId={(flag) => flag.key}
    />
  );
}
