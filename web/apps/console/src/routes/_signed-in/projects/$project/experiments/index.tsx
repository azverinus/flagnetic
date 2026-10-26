import type { Experiment } from "@flagnetic/api";
import { plural } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { BellIcon, ClockAlertIcon, FlaskConicalIcon, PlusIcon } from "lucide-react";
import { type ReactNode, useId, useMemo, useState } from "react";

import { Can } from "@/components/can";
import { ExperimentStatusBadge } from "@/components/experiment-status-badge";
import { QueryError } from "@/components/query-error";
import { SearchField } from "@/components/search-field";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Field } from "@/components/ui/field";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/ui/page-header";
import { Select, SelectItem } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatDay } from "@/i18n/format";
import { awaitingAcknowledgement, isOverdue } from "@/lib/experiment-alerts";
import {
  experimentStatuses,
  experimentStatusName,
  type ExperimentsSearch,
  holdingEnvironments,
  isExperimentsFiltered,
  validateExperimentsSearch,
} from "@/lib/experiments";
import { madeBy } from "@/lib/project-members";
import {
  environmentsQuery,
  experimentAlertsQuery,
  experimentsQuery,
  flagChoicesQuery,
  flagQuery,
} from "@/lib/queries";
import { useKnownMembers } from "@/lib/use-known-members";
import { useNowPassing } from "@/lib/use-now";

export const Route = createFileRoute("/_signed-in/projects/$project/experiments/")({
  validateSearch: validateExperimentsSearch,
  component: Experiments,
});

/** The select's value for a filter left unset: Radix keeps the empty string for itself. */
const any = "any";

/**
 * A project's experiments, filtered by what the address holds and read a
 * page at a time; creating one is offered to those who may.
 */
function Experiments() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const { names } = useKnownMembers(project);

  // Filters and pages replace the address rather than adding to the
  // history, since the pages seen are remembered on the screen: going
  // back leaves the list instead of walking through its pages.
  const apply = (next: ExperimentsSearch) => void navigate({ search: next, replace: true });
  const filter = (patch: Partial<ExperimentsSearch>) =>
    apply({ ...search, ...patch, cursor: undefined });

  const newExperiment = (
    <Can permission="experiment.edit" whenMissing="disable">
      <Button asChild>
        <Link to="/projects/$project/experiments/-/new" params={{ project }}>
          <PlusIcon aria-hidden />
          <Trans>New experiment</Trans>
        </Link>
      </Button>
    </Can>
  );

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Experiments`}
        description={
          <Trans>
            Each experiment compares variations of one flag over the units of one unit type; a
            flag's rule serves it in the environments that hold it.
          </Trans>
        }
        actions={newExperiment}
      />
      <ExperimentFilters project={project} search={search} onFilter={filter} owners={names} />
      <ExperimentsPages
        project={project}
        search={search}
        onPage={(cursor) => apply({ ...search, cursor })}
        onClear={() => apply({})}
        newExperiment={newExperiment}
        owners={names}
      />
    </div>
  );
}

function ExperimentFilters({
  project,
  search,
  onFilter,
  owners,
}: {
  project: string;
  search: ExperimentsSearch;
  onFilter: (patch: Partial<ExperimentsSearch>) => void;
  owners: ReadonlyMap<string, string> | undefined;
}) {
  const { i18n, t } = useLingui();
  const archived = useId();
  const overdue = useId();
  const awaiting = useId();
  const flags = useQuery(flagChoicesQuery(project));
  const ownerOptions = useMemo(
    () =>
      [...(owners ?? new Map<string, string>())]
        .map(([value, label]) => ({ value, label }))
        .sort((a, b) => a.label.localeCompare(b.label, i18n.locale)),
    [owners, i18n.locale],
  );
  const flagKeys = (flags.data ?? []).map((flag) => flag.key);

  return (
    <div
      role="search"
      aria-label={t`Filter the experiments`}
      className="flex flex-wrap items-end gap-4"
    >
      <Field label={<Trans>Status</Trans>} className="w-40">
        <Select
          value={search.status ?? any}
          onValueChange={(value) =>
            onFilter({ status: experimentStatuses.find((status) => status === value) })
          }
        >
          <SelectItem value={any}>{t`Any status`}</SelectItem>
          {experimentStatuses.map((status) => (
            <SelectItem key={status} value={status}>
              {experimentStatusName(i18n, status)}
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
              archived one, whose experiments are still listed. */}
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
      <Field label={<Trans>Owner</Trans>} className="w-56">
        <Select
          value={search.owner ?? any}
          onValueChange={(value) => onFilter({ owner: value === any ? undefined : value })}
        >
          <SelectItem value={any}>{t`Any owner`}</SelectItem>
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
      <SearchField
        label={<Trans>Tag</Trans>}
        placeholder={t`The whole tag`}
        className="w-48"
        value={search.tag ?? ""}
        onChange={(tag) => onFilter({ tag: tag.trim() || undefined })}
      />
      <div className="flex h-9 items-center gap-2">
        <Switch
          id={archived}
          checked={search.archived === true}
          onCheckedChange={(checked) => onFilter({ archived: checked ? true : undefined })}
        />
        <Label htmlFor={archived}>
          <Trans>Include archived experiments</Trans>
        </Label>
      </div>
      <div className="flex h-9 items-center gap-2">
        <Switch
          id={overdue}
          checked={search.past_expected_end === true}
          onCheckedChange={(checked) => onFilter({ past_expected_end: checked ? true : undefined })}
        />
        <Label htmlFor={overdue}>
          <Trans>Past expected end</Trans>
        </Label>
      </div>
      <div className="flex h-9 items-center gap-2">
        <Switch
          id={awaiting}
          checked={search.awaiting_code_removal === true}
          onCheckedChange={(checked) =>
            onFilter({ awaiting_code_removal: checked ? true : undefined })
          }
        />
        <Label htmlFor={awaiting}>
          <Trans>Awaiting code removal</Trans>
        </Label>
      </div>
    </div>
  );
}

/**
 * The experiments of one search, a page at a time. The server pages
 * forward only, by the cursor in the address; the cursors of the pages
 * seen before are kept here, so "Previous" goes back one page at a time.
 */
function ExperimentsPages({
  project,
  search,
  onPage,
  onClear,
  newExperiment,
  owners,
}: {
  project: string;
  search: ExperimentsSearch;
  onPage: (cursor: string | undefined) => void;
  onClear: () => void;
  newExperiment: ReactNode;
  owners: ReadonlyMap<string, string> | undefined;
}) {
  const { t } = useLingui();
  const page = useQuery(experimentsQuery(project, search));
  // The cursor of every page before this one, the first page's as none;
  // other filters start again from their first page.
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
    return isExperimentsFiltered(search) ? (
      <EmptyState
        icon={<FlaskConicalIcon />}
        title={<Trans>No experiment matches these filters.</Trans>}
        action={
          <Button variant="outline" onClick={onClear}>
            <Trans>Clear filters</Trans>
          </Button>
        }
      />
    ) : (
      <EmptyState
        icon={<FlaskConicalIcon />}
        title={<Trans>This project has no experiment yet.</Trans>}
        description={
          <Trans>An experiment starts as a draft and changes nothing until it is started.</Trans>
        }
        action={newExperiment}
      />
    );
  }

  const next = page.data?.next_cursor ?? undefined;
  const busy = page.isFetching;
  return (
    <>
      <ExperimentsTable
        project={project}
        experiments={page.data?.items ?? []}
        loading={page.isPending}
        owners={owners}
      />
      <nav aria-label={t`Pages of experiments`} className="flex flex-wrap justify-end gap-2">
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
 * The environments whose rules serve an experiment, read from its flag:
 * the experiment resource does not name them. Every row of one flag
 * shares the one read.
 */
function HoldingEnvironments({ project, experiment }: { project: string; experiment: Experiment }) {
  const { t } = useLingui();
  const flag = useQuery(flagQuery(project, experiment.flag));
  const environments = useQuery(environmentsQuery(project));
  if (flag.isError || environments.isError) {
    return <span className="text-muted-foreground">{t`Could not be read`}</span>;
  }
  if (flag.data === undefined || environments.data === undefined) {
    return <Skeleton className="h-4 w-20" />;
  }
  const keys = holdingEnvironments(flag.data, experiment.key);
  if (keys.length === 0) {
    return <span className="text-muted-foreground">{t`No rule serves it`}</span>;
  }
  const nameOf = new Map(
    environments.data.map((environment) => [environment.key, environment.name]),
  );
  return (
    <ul aria-label={t`Holding environments`} className="flex flex-wrap gap-1">
      {keys.map((key) => (
        <li key={key} className="rounded-sm bg-muted px-1.5 py-0.5 text-xs">
          {nameOf.get(key) ?? key}
        </li>
      ))}
    </ul>
  );
}

/**
 * What needs a look in a row: running past the expected end, judged from
 * the date as the `past_expected_end` filter judges it, and the active
 * alerts nobody has acknowledged, read per experiment since the
 * experiment resource does not carry them. A draft is never checked, so
 * its alerts are not read.
 */
function AttentionMarkers({
  project,
  experiment,
  now,
}: {
  project: string;
  experiment: Experiment;
  now: Date;
}) {
  const { t } = useLingui();
  const alerts = useQuery({
    ...experimentAlertsQuery(project, experiment.key, { active: true }),
    enabled: experiment.status !== "draft",
  });
  const overdue = isOverdue(experiment, now);
  const unacknowledged = alerts.data ? awaitingAcknowledgement(alerts.data.items).length : 0;
  if (!overdue && unacknowledged === 0) return null;
  return (
    <ul aria-label={t`Needs attention`} className="flex flex-wrap gap-1">
      {overdue ? (
        <li>
          <Badge variant="muted" className="flex items-center gap-1 whitespace-nowrap">
            <ClockAlertIcon aria-hidden className="size-3" />
            <Trans>Overdue</Trans>
          </Badge>
        </li>
      ) : null}
      {unacknowledged > 0 ? (
        <li>
          <Badge className="flex items-center gap-1 whitespace-nowrap border-destructive/50 text-destructive">
            <BellIcon aria-hidden className="size-3" />
            {t`${plural(unacknowledged, { one: "# alert", other: "# alerts" })}`}
          </Badge>
        </li>
      ) : null}
    </ul>
  );
}

function ExperimentsTable({
  project,
  experiments,
  loading,
  owners,
}: {
  project: string;
  experiments: Experiment[];
  loading: boolean;
  owners: ReadonlyMap<string, string> | undefined;
}) {
  const { i18n, t } = useLingui();
  // Overdue changes with the UTC day; the page read since is near enough.
  const now = useNowPassing([]);
  const columns = useMemo<TableColumn<Experiment>[]>(
    () => [
      {
        id: "experiment",
        header: t`Experiment`,
        cell: ({ row }) => (
          <Link
            to="/projects/$project/experiments/$experiment"
            params={{ project, experiment: row.original.key }}
            className="flex flex-col hover:underline"
          >
            <code className="font-mono text-xs">{row.original.key}</code>
            <span className="text-muted-foreground">{row.original.name}</span>
          </Link>
        ),
      },
      {
        id: "status",
        header: t`Status`,
        cell: ({ row }) => (
          <span className="flex flex-col items-start gap-1">
            <ExperimentStatusBadge status={row.original.status} />
            <AttentionMarkers project={project} experiment={row.original} now={now} />
          </span>
        ),
      },
      {
        id: "flag",
        header: t`Flag`,
        cell: ({ row }) => (
          <Link
            to="/projects/$project/flags/$flag"
            params={{ project, flag: row.original.flag }}
            className="font-mono text-xs hover:underline"
          >
            {row.original.flag}
          </Link>
        ),
      },
      {
        id: "holding",
        header: t`Holding environments`,
        cell: ({ row }) => <HoldingEnvironments project={project} experiment={row.original} />,
      },
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
        id: "expected_end",
        header: t`Expected end`,
        cell: ({ row }) =>
          row.original.expected_end_at ? (
            <time dateTime={row.original.expected_end_at} className="whitespace-nowrap">
              {formatDay(i18n, row.original.expected_end_at)}
            </time>
          ) : (
            <span className="text-muted-foreground">—</span>
          ),
      },
    ],
    [i18n, now, owners, project, t],
  );
  return (
    <Table
      caption={t`Experiments`}
      hideCaption
      stickyFirstColumn
      columns={columns}
      data={experiments}
      loading={loading}
      getRowId={(experiment) => experiment.id}
    />
  );
}
