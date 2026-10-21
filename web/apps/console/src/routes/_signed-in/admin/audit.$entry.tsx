import { useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useMemo } from "react";

import { AuditEntrySheet } from "@/components/audit-entry-sheet";
import { validateAuditSearch } from "@/lib/audit";
import { ApiError } from "@/lib/errors";
import { auditEntriesQuery, projectAuditEntryQuery, projectsQuery } from "@/lib/queries";

type EntrySearch = {
  /** The key of the project the entry belongs to, when it belongs to one that still exists. */
  project_key?: string;
};

/**
 * One entry of the instance's audit log, over the log. An entry of a
 * project is read through that project's entry read, so a shared link
 * keeps working. The instance's log has no read of one entry, so any
 * other entry is taken from the page the address names: the same filters
 * and cursor read the same page again, and once the entry has left it the
 * link shows not found.
 */
export const Route = createFileRoute("/_signed-in/admin/audit/$entry")({
  validateSearch: (search: Record<string, unknown>): EntrySearch =>
    typeof search.project_key === "string" && search.project_key !== ""
      ? { project_key: search.project_key }
      : {},
  component: InstanceAuditEntry,
});

function InstanceAuditEntry() {
  const { t } = useLingui();
  const { entry: id } = Route.useParams();
  const address = Route.useSearch();
  const projectKey = address.project_key;
  // The page the log shows, by its own filters alone: the same query, so
  // the entry is found in what the log has already read.
  const search = useMemo(() => validateAuditSearch(address), [address]);
  const navigate = Route.useNavigate();
  const projects = useQuery(projectsQuery);
  const read = useQuery({
    ...projectAuditEntryQuery(projectKey ?? "", id),
    enabled: projectKey !== undefined,
  });
  const page = useQuery({
    ...auditEntriesQuery("instance", search),
    enabled: projectKey === undefined,
  });

  const found =
    projectKey === undefined ? page.data?.items.find((item) => item.id === id) : read.data;
  const missing =
    projectKey === undefined && page.isSuccess && !page.isPlaceholderData && found === undefined
      ? new ApiError(404, { code: "not_found" })
      : undefined;
  const projectId = found?.project_id;
  const project = projectId
    ? (projects.data?.find((item) => item.id === projectId)?.name ?? projectId)
    : t({ message: "Instance", comment: "An audit entry that belongs to no project." });

  return (
    <AuditEntrySheet
      entry={found}
      pending={projectKey === undefined ? page.isPending || page.isPlaceholderData : read.isPending}
      error={projectKey === undefined ? (page.error ?? missing) : read.error}
      onRetry={() => void (projectKey === undefined ? page.refetch() : read.refetch())}
      place={found ? { label: t`Project`, value: project } : undefined}
      onClose={() => void navigate({ to: "/admin/audit", search })}
    />
  );
}
