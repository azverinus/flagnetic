import type { AuditEntry } from "@flagnetic/api";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { createContext, type ReactNode, useCallback, useContext, useMemo } from "react";

import { AuditLog, type AuditLogProps } from "@/components/audit-log";
import { QueryError } from "@/components/query-error";
import { PageHeader } from "@/components/ui/page-header";
import { type AuditSearch, validateAuditSearch } from "@/lib/audit";
import { membersQuery, projectsQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/admin/audit")({
  validateSearch: validateAuditSearch,
  staticData: { crumb: msg`Audit log` },
  component: InstanceAudit,
});

/**
 * Every entry of the instance: those of every project and those that
 * belong to none, such as members, sign-ins and settings.
 */
function InstanceAudit() {
  const { t } = useLingui();
  const address = Route.useSearch();
  // A route's search also carries what its children read, such as an open
  // entry's project key; the listing, its cache key and the filter form
  // are built from the log's own filters alone, so opening an entry does
  // not read the page again, send the key along or count as a filter.
  const search = useMemo(() => validateAuditSearch(address), [address]);
  const navigate = Route.useNavigate();
  const members = useQuery(membersQuery);
  const projects = useQuery(projectsQuery);

  const onSearch = useCallback((next: AuditSearch) => void navigate({ search: next }), [navigate]);
  const actors = useMemo(
    () =>
      (members.data ?? []).map((member) => ({
        value: member.id,
        label: member.display_name || member.email,
      })),
    [members.data],
  );
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
  const place = useMemo<AuditLogProps["place"]>(() => {
    const names = new Map((projects.data ?? []).map((project) => [project.id, project.name]));
    const instance = t({
      message: "Instance",
      comment: "An audit entry that belongs to no project.",
    });
    return {
      filter: "project",
      label: t`Project`,
      options: (projects.data ?? []).map((project) => ({ value: project.id, label: project.name })),
      // A deleted project's entries stay; they show its identifier.
      of: (entry) =>
        entry.project_id ? (names.get(entry.project_id) ?? entry.project_id) : instance,
    };
  }, [projects.data, t]);

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Audit log`}
        description={
          <Trans>
            Every change on this instance, in every project and outside them, such as members,
            sign-ins and settings.
          </Trans>
        }
      />
      {projects.isError ? (
        <QueryError error={projects.error} onRetry={() => void projects.refetch()} />
      ) : null}
      <ProjectKeys.Provider value={projectKeys}>
        <AuditLog
          scope="instance"
          search={search}
          onSearch={onSearch}
          EntryLink={InstanceAuditEntryLink}
          actors={actors}
          place={place}
        />
      </ProjectKeys.Provider>
      <Outlet />
    </div>
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
