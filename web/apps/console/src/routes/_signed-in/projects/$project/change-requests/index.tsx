import type { ChangeRequest } from "@flagnetic/api";
import { Card } from "@/components/ui/card";
import { Page } from "@/components/ui/page";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { GitPullRequestArrowIcon, LockIcon } from "lucide-react";
import { useMemo, useState } from "react";

import { ChangeRequestStateBadge } from "@/components/change-request-state-badge";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { EnvironmentName } from "@/components/protected-mark";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Table, type TableColumn } from "@/components/ui/table";
import { Toolbar } from "@/components/ui/toolbar";
import { ChangeRequestsPanel, useChangeRequestsFilters } from "@/components/change-requests-panel";
import { formatDateTime } from "@/i18n/format";
import { useCan, useMissingPermissionReason } from "@/lib/access";
import {
  type ChangeRequestsSearch,
  isChangeRequestsFiltered,
  validateChangeRequestsSearch,
} from "@/lib/change-requests";
import { changeRequestsQuery, environmentsQuery, meQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/projects/$project/change-requests/")({
  validateSearch: validateChangeRequestsSearch,
  staticData: { panel: ChangeRequestsPanel },
  component: ChangeRequests,
});

/**
 * A project's change requests: the view and the facets are in the context
 * panel, their chips in the toolbar; the list is read a page at a time.
 */
function ChangeRequests() {
  const { t } = useLingui();
  const { project, search, apply, chips, removeChip, clearFacets } = useChangeRequestsFilters();
  return (
    <Page>
      <PageHeader
        title={t`Change requests`}
        description={
          <Trans>
            A change request proposes a change for review; once a member who did not write it
            approves it, it can be applied.
          </Trans>
        }
      />
      <Toolbar chips={chips} onRemoveChip={removeChip} onClearAll={clearFacets} />
      <ChangeRequestPages
        project={project}
        search={search}
        // Filters and pages replace the address rather than adding to the
        // history, since the pages seen are remembered on the screen: going
        // back leaves the list instead of walking through its pages.
        onPage={(cursor) => apply({ ...search, cursor })}
        onClear={clearFacets}
      />
    </Page>
  );
}

/**
 * The change requests of one search, a page at a time. The server pages
 * forward only, by the cursor in the address; the cursors of the pages
 * seen before are kept here, so "Previous" goes back one page at a time.
 */
function ChangeRequestPages({
  project,
  search,
  onPage,
  onClear,
}: {
  project: string;
  search: ChangeRequestsSearch;
  onPage: (cursor: string | undefined) => void;
  onClear: () => void;
}) {
  const { t } = useLingui();
  const canDecide = useCan("change_request.approve");
  const missing = useMissingPermissionReason("change_request.approve");
  const me = useQuery(meQuery);
  const member = me.data?.member?.id;
  // A caller who is not a member, such as an instance admin acting
  // without one, has written no change request.
  const authorless = search.tab === "mine" && me.isSuccess && member === undefined;
  const page = useQuery({
    ...changeRequestsQuery(project, search, member),
    // The caller's own are asked for by the caller's identifier, known
    // once the caller is read.
    enabled: search.tab !== "mine" || member !== undefined,
  });
  const { cursor: _page, ...filters } = search;
  const filtersSeen = JSON.stringify(filters);
  const [pages, setPages] = useState<{ of: string; seen: (string | undefined)[] }>({
    of: filtersSeen,
    seen: [],
  });
  if (pages.of !== filtersSeen) setPages({ of: filtersSeen, seen: [] });
  const seen = pages.of === filtersSeen ? pages.seen : [];
  const setSeen = (next: (string | undefined)[]) => setPages({ of: filtersSeen, seen: next });

  if (search.tab === "awaiting" && !canDecide) {
    return (
      <Card list>
        <EmptyState
          icon={<LockIcon />}
          title={<Trans>No change request awaits your decision.</Trans>}
          description={missing}
        />
      </Card>
    );
  }
  if (authorless) {
    return (
      <Card list>
        <EmptyState
          icon={<GitPullRequestArrowIcon />}
          title={<Trans>You have proposed no change request here.</Trans>}
        />
      </Card>
    );
  }
  if (page.isError) {
    return <QueryError error={page.error} onRetry={() => void page.refetch()} />;
  }
  if (page.data?.items.length === 0 && !search.cursor) {
    if (isChangeRequestsFiltered(search)) {
      return (
        <Card list>
          <EmptyState
            icon={<GitPullRequestArrowIcon />}
            title={<Trans>No change request matches these filters.</Trans>}
            action={
              <Button variant="outline" onClick={onClear}>
                <Trans>Clear filters</Trans>
              </Button>
            }
          />
        </Card>
      );
    }
    return (
      <Card list>
        <EmptyState
          icon={<GitPullRequestArrowIcon />}
          title={
            search.tab === "awaiting" ? (
              <Trans>No change request awaits your decision.</Trans>
            ) : search.tab === "mine" ? (
              <Trans>You have proposed no change request here.</Trans>
            ) : (
              <Trans>This project has no change request yet.</Trans>
            )
          }
          description={
            <Trans>
              A change to a protected environment is proposed as a change request from the flag's
              configuration.
            </Trans>
          }
        />
      </Card>
    );
  }

  const next = page.data?.next_cursor ?? undefined;
  const busy = page.isFetching;
  return (
    <>
      <ChangeRequestsTable
        project={project}
        changeRequests={page.data?.items ?? []}
        loading={page.isPending}
      />
      <nav aria-label={t`Pages of change requests`} className="flex flex-wrap justify-end gap-2">
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

function ChangeRequestsTable({
  project,
  changeRequests,
  loading,
}: {
  project: string;
  changeRequests: ChangeRequest[];
  loading: boolean;
}) {
  const { i18n, t } = useLingui();
  const environments = useQuery(environmentsQuery(project));
  const columns = useMemo<TableColumn<ChangeRequest>[]>(() => {
    const environmentName = (key: string) => {
      const environment = environments.data?.find((candidate) => candidate.key === key);
      return environment ? <EnvironmentName environment={environment} /> : key;
    };
    return [
      {
        id: "title",
        header: t`Change request`,
        cell: ({ row }) => (
          <Link
            to="/projects/$project/change-requests/$changeRequest"
            params={{ project, changeRequest: row.original.id }}
            className="font-medium hover:underline"
          >
            {row.original.title}
          </Link>
        ),
      },
      {
        id: "state",
        header: t`State`,
        cell: ({ row }) => <ChangeRequestStateBadge state={row.original.state} />,
      },
      {
        id: "apply_at",
        header: t`Applies at`,
        cell: ({ row }) => {
          const { apply_at, schedule_failure, state } = row.original;
          // Only a change request that can still apply has a time ahead of it.
          if (apply_at === null || (state !== "draft" && state !== "open" && state !== "approved"))
            return null;
          return (
            <span className="flex flex-col whitespace-nowrap">
              <time dateTime={apply_at}>{formatDateTime(i18n, apply_at)}</time>
              {schedule_failure !== null ? (
                <span className="text-xs text-destructive">
                  {schedule_failure.stopped ? t`Failed, stopped` : t`Failed`}
                </span>
              ) : null}
            </span>
          );
        },
      },
      {
        id: "flag",
        header: t`Flag`,
        cell: ({ row }) =>
          row.original.flag ? (
            <Link
              to="/projects/$project/flags/$flag"
              params={{ project, flag: row.original.flag }}
              className="font-mono text-xs hover:underline"
            >
              {row.original.flag}
            </Link>
          ) : null,
      },
      {
        id: "environment",
        header: t`Environment`,
        cell: ({ row }) =>
          row.original.environment ? environmentName(row.original.environment) : null,
      },
      {
        id: "author",
        header: t`Author`,
        cell: ({ row }) => row.original.author.name,
      },
      {
        id: "updated",
        header: t`Updated`,
        cell: ({ row }) => (
          <time dateTime={row.original.updated_at} className="whitespace-nowrap">
            {formatDateTime(i18n, row.original.updated_at)}
          </time>
        ),
      },
    ];
  }, [environments.data, i18n, project, t]);
  return (
    <Card list>
      <Table
        caption={t`Change requests`}
        hideCaption
        stickyFirstColumn
        columns={columns}
        data={changeRequests}
        loading={loading}
        getRowId={(changeRequest) => changeRequest.id}
      />
    </Card>
  );
}
