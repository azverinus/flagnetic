import type { ChangeRequest } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { GitPullRequestArrowIcon, LockIcon } from "lucide-react";
import { useMemo, useState } from "react";

import { ChangeRequestStateBadge } from "@/components/change-request-state-badge";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Field } from "@/components/ui/field";
import { PageHeader } from "@/components/ui/page-header";
import { Select, SelectItem } from "@/components/ui/select";
import { Table, type TableColumn } from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import { formatDateTime } from "@/i18n/format";
import { useCan, useMissingPermissionReason } from "@/lib/access";
import {
  changeRequestKindName,
  changeRequestKinds,
  type ChangeRequestsSearch,
  changeRequestStateName,
  changeRequestStates,
  type ChangeRequestsTab,
  isChangeRequestsFiltered,
  validateChangeRequestsSearch,
} from "@/lib/change-requests";
import { changeRequestsQuery, environmentsQuery, flagChoicesQuery, meQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/projects/$project/change-requests/")({
  validateSearch: validateChangeRequestsSearch,
  component: ChangeRequests,
});

/** The select's value for a filter left unset: Radix keeps the empty string for itself. */
const any = "any";

/**
 * A project's change requests in three tabs: those awaiting the caller's
 * decision, the caller's own, and all of them; each filtered by what the
 * address holds and read a page at a time.
 */
function ChangeRequests() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();

  // Filters and pages replace the address rather than adding to the
  // history, since the pages seen are remembered on the screen: going
  // back leaves the list instead of walking through its pages.
  const apply = (next: ChangeRequestsSearch) => void navigate({ search: next, replace: true });
  const filter = (patch: Partial<ChangeRequestsSearch>) =>
    apply({ ...search, ...patch, cursor: undefined });
  const tab: ChangeRequestsTab = search.tab ?? "all";

  const content = (
    <div className="flex flex-col gap-6">
      <ChangeRequestFilters project={project} search={search} onFilter={filter} />
      <ChangeRequestPages
        project={project}
        search={search}
        onPage={(cursor) => apply({ ...search, cursor })}
        onClear={() => apply({ tab: search.tab })}
      />
    </div>
  );
  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Change requests`}
        description={
          <Trans>
            A change request proposes a change for review; once a member who did not write it
            approves it, it can be applied.
          </Trans>
        }
      />
      <Tabs
        label={t`Which change requests`}
        value={tab}
        onValueChange={(next) =>
          filter({
            tab: next === "awaiting" || next === "mine" ? next : undefined,
            // Only open change requests await a decision.
            ...(next === "awaiting" ? { state: undefined } : {}),
          })
        }
        tabs={[
          { value: "awaiting", label: t`Awaiting my decision`, content },
          { value: "mine", label: t`Mine`, content },
          { value: "all", label: t`All`, content },
        ]}
      />
    </div>
  );
}

function ChangeRequestFilters({
  project,
  search,
  onFilter,
}: {
  project: string;
  search: ChangeRequestsSearch;
  onFilter: (patch: Partial<ChangeRequestsSearch>) => void;
}) {
  const { i18n, t } = useLingui();
  const flags = useQuery(flagChoicesQuery(project));
  const environments = useQuery(environmentsQuery(project));
  const flagKeys = (flags.data ?? []).map((flag) => flag.key);

  return (
    <div
      role="search"
      aria-label={t`Filter the change requests`}
      className="flex flex-wrap items-end gap-4"
    >
      {search.tab === "awaiting" ? null : (
        <Field label={<Trans>State</Trans>} className="w-40">
          <Select
            value={search.state ?? any}
            onValueChange={(value) =>
              onFilter({ state: changeRequestStates.find((state) => state === value) })
            }
          >
            <SelectItem value={any}>{t`Any state`}</SelectItem>
            {changeRequestStates.map((state) => (
              <SelectItem key={state} value={state}>
                {changeRequestStateName(i18n, state)}
              </SelectItem>
            ))}
          </Select>
        </Field>
      )}
      <Field label={<Trans>Kind</Trans>} className="w-48">
        <Select
          value={search.kind ?? any}
          onValueChange={(value) =>
            onFilter({ kind: changeRequestKinds.find((kind) => kind === value) })
          }
        >
          <SelectItem value={any}>{t`Any kind`}</SelectItem>
          {changeRequestKinds.map((kind) => (
            <SelectItem key={kind} value={kind}>
              {changeRequestKindName(i18n, kind)}
            </SelectItem>
          ))}
        </Select>
      </Field>
      <Field label={<Trans>Flag</Trans>} className="w-56">
        <Select
          value={search.flag ?? any}
          onValueChange={(value) => onFilter({ flag: value === any ? undefined : value })}
        >
          <SelectItem value={any}>{t`Any flag`}</SelectItem>
          {/* A flag the address names that is not among those read: an
              archived one, whose change requests are still listed. */}
          {search.flag && !flagKeys.includes(search.flag) ? (
            <SelectItem value={search.flag}>{search.flag}</SelectItem>
          ) : null}
          {flagKeys.map((key) => (
            <SelectItem key={key} value={key}>
              {key}
            </SelectItem>
          ))}
        </Select>
      </Field>
      <Field label={<Trans>Environment</Trans>} className="w-48">
        <Select
          value={search.environment ?? any}
          onValueChange={(value) => onFilter({ environment: value === any ? undefined : value })}
        >
          <SelectItem value={any}>{t`Any environment`}</SelectItem>
          {(environments.data ?? []).map((environment) => (
            <SelectItem key={environment.key} value={environment.key}>
              {environment.name}
            </SelectItem>
          ))}
        </Select>
      </Field>
    </div>
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
      <EmptyState
        icon={<LockIcon />}
        title={<Trans>No change request awaits your decision.</Trans>}
        description={missing}
      />
    );
  }
  if (page.isError) {
    return <QueryError error={page.error} onRetry={() => void page.refetch()} />;
  }
  if (page.data?.items.length === 0 && !search.cursor) {
    if (isChangeRequestsFiltered(search)) {
      return (
        <EmptyState
          icon={<GitPullRequestArrowIcon />}
          title={<Trans>No change request matches these filters.</Trans>}
          action={
            <Button variant="outline" onClick={onClear}>
              <Trans>Clear filters</Trans>
            </Button>
          }
        />
      );
    }
    return (
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
    const environmentName = (key: string) =>
      environments.data?.find((environment) => environment.key === key)?.name ?? key;
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
    <Table
      caption={t`Change requests`}
      hideCaption
      stickyFirstColumn
      columns={columns}
      data={changeRequests}
      loading={loading}
      getRowId={(changeRequest) => changeRequest.id}
    />
  );
}
