import type { AuditEntry } from "@flagnetic/api";
import { Page } from "@/components/ui/page";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { createContext, type ReactNode, useContext, useMemo } from "react";

import { InstanceAuditPanel, useInstanceAuditFilters } from "@/components/audit-filters";
import { AuditLog } from "@/components/audit-log";
import { QueryError } from "@/components/query-error";
import { PageHeader } from "@/components/ui/page-header";
import { type AuditSearch, validateAuditSearch } from "@/lib/audit";
import { projectsQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/admin/audit")({
  validateSearch: validateAuditSearch,
  staticData: { crumb: msg`Audit log`, panel: InstanceAuditPanel },
  component: InstanceAudit,
});

/**
 * Every entry of the instance: those of every project and those that
 * belong to none, such as members, sign-ins and settings.
 */
function InstanceAudit() {
  const { t } = useLingui();
  // A route's search also carries what its children read, such as an open
  // entry's project key; the filters take the log's own alone, so opening
  // an entry does not read the page again, send the key along or count as
  // a filter.
  const filters = useInstanceAuditFilters();
  const projects = useQuery(projectsQuery);
  // Resolved once for every row: a project entry's link carries its
  // project's key, so it waits for the projects, and goes without a link
  // while they cannot be read, rather than linking without the key.
  const projectKeys = useMemo(
    () =>
      projects.data === undefined
        ? undefined
        : new Map(projects.data.map((project) => [project.id, project.key])),
    [projects.data],
  );

  return (
    <Page>
      <PageHeader
        title={t`Audit log`}
        description={
          <Trans>Every change on this instance, in every project and outside them.</Trans>
        }
      />
      {projects.isError ? (
        <QueryError error={projects.error} onRetry={() => void projects.refetch()} />
      ) : null}
      <ProjectKeys.Provider value={projectKeys}>
        <AuditLog filters={filters} EntryLink={InstanceAuditEntryLink} />
      </ProjectKeys.Provider>
      <Outlet />
    </Page>
  );
}

/**
 * Every project's key by its identifier, once the projects are read; none
 * while they are being read or after reading them failed.
 */
const ProjectKeys = createContext<ReadonlyMap<string, string> | undefined>(undefined);

/**
 * The link to one entry of the instance's audit log, keeping the log's
 * filters. An entry of a project carries the project's key, so its detail
 * is read on its own and a shared link keeps working once the entry has
 * left the page its filters name; while the keys are being read, or
 * cannot be, such an entry is shown without a link. An entry of a project that is gone has no key
 * and is found in the page, as one of no project is.
 */
function InstanceAuditEntryLink({
  entry,
  className,
  children,
}: {
  entry: AuditEntry;
  className?: string;
  children: ReactNode;
}) {
  const projectKeys = useContext(ProjectKeys);
  if (entry.project_id && projectKeys === undefined) {
    return <span className={className}>{children}</span>;
  }
  const projectKey = entry.project_id ? projectKeys?.get(entry.project_id) : undefined;
  return (
    <Link
      to="/admin/audit/$entry"
      params={{ entry: entry.id }}
      search={(current: AuditSearch) => ({
        ...validateAuditSearch(current),
        project_key: projectKey,
      })}
      className={className}
    >
      {children}
    </Link>
  );
}
