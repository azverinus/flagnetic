import type { AuditEntry } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { Link, type LinkProps, type RegisteredRouter, useParams } from "@tanstack/react-router";
import { type ComponentProps, useId, useMemo } from "react";

import { AuditEntriesTable, type EntryLinkComponent, type Place } from "@/components/audit-log";
import { QueryError } from "@/components/query-error";
import { type AuditSearch, validateProjectAuditSearch } from "@/lib/audit";
import { auditEntriesQuery, environmentsQuery } from "@/lib/queries";

/**
 * An environment's name by its identifier, for what an audit entry names;
 * the identifier itself for one the project no longer has, since entries
 * outlive what they name.
 */
export function useEnvironmentName(project: string): (id: string) => string {
  const environments = useQuery(environmentsQuery(project));
  return useMemo(() => {
    const names = new Map(
      (environments.data ?? []).map((environment) => [environment.id, environment.name]),
    );
    return (id: string) => names.get(id) ?? id;
  }, [environments.data]);
}

/** The environment column of a project's audit entries. */
export function useEnvironmentPlace(project: string): Place {
  const { t } = useLingui();
  const environmentName = useEnvironmentName(project);
  return useMemo(
    () => ({
      label: t`Environment`,
      of: (entry: AuditEntry) =>
        entry.environment_id ? environmentName(entry.environment_id) : "",
    }),
    [environmentName, t],
  );
}

type EntryLinkProps = ComponentProps<EntryLinkComponent>;

/** What an entry of the project's audit log opens with: the log's filters, or where it was opened from. */
type EntrySearch = LinkProps<
  "a",
  RegisteredRouter,
  string,
  "/projects/$project/audit/$entry"
>["search"];

const keepFilters = (current: AuditSearch) => validateProjectAuditSearch(current);

/**
 * The link to one entry of the open project's audit log, opened with
 * `search`: by default the log's filters, kept.
 */
export function ProjectAuditEntryLink({
  search = keepFilters,
  entry,
  className,
  children,
}: EntryLinkProps & { search?: EntrySearch }) {
  const { project } = useParams({ from: "/_signed-in/projects/$project" });
  return (
    <Link
      to="/projects/$project/audit/$entry"
      params={{ project, entry: entry.id }}
      search={search}
      className={className}
    >
      {children}
    </Link>
  );
}

/**
 * The link to one entry from the overview's recent activity: the entry
 * opens over the log, and closing it goes back to the overview.
 */
function RecentEntryLink(props: EntryLinkProps) {
  return <ProjectAuditEntryLink {...props} search={{ opened_from: "overview" }} />;
}

/** How many entries the overview shows. */
const recentEntries = 10;

const unfiltered: AuditSearch = {};

/**
 * The newest entries of the project's audit log, on its overview, with
 * the way to the whole log.
 */
export function RecentActivity({ project }: { project: string }) {
  const { t } = useLingui();
  const heading = useId();
  const entries = useQuery(auditEntriesQuery({ project }, unfiltered, recentEntries));
  const place = useEnvironmentPlace(project);

  return (
    <section aria-labelledby={heading} className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-4">
        <h2 id={heading} className="text-base font-semibold">
          <Trans>Recent activity</Trans>
        </h2>
        <Link
          to="/projects/$project/audit"
          params={{ project }}
          className="text-sm text-muted-foreground hover:text-foreground hover:underline"
        >
          <Trans>View the audit log</Trans>
        </Link>
      </div>
      {entries.isError ? (
        <QueryError error={entries.error} onRetry={() => void entries.refetch()} />
      ) : entries.data?.items.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          <Trans>Nothing has changed in this project yet.</Trans>
        </p>
      ) : (
        <AuditEntriesTable
          caption={t`The most recent changes in this project`}
          entries={entries.data?.items ?? []}
          loading={entries.isPending}
          EntryLink={RecentEntryLink}
          place={place}
        />
      )}
    </section>
  );
}
