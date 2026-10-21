import { useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";

import { AuditEntrySheet } from "@/components/audit-entry-sheet";
import { useEnvironmentName } from "@/components/project-audit";
import { validateProjectAuditSearch } from "@/lib/audit";
import { projectAuditEntryQuery } from "@/lib/queries";

type EntrySearch = {
  /**
   * Where the entry was opened from when it was not the log, so closing it
   * goes back there: the overview's recent activity.
   */
  opened_from?: "overview";
};

/** One entry of the project's audit log, over the log, the filters kept. */
export const Route = createFileRoute("/_signed-in/projects/$project/audit/$entry")({
  validateSearch: (search: Record<string, unknown>): EntrySearch =>
    search.opened_from === "overview" ? { opened_from: "overview" } : {},
  component: ProjectAuditEntry,
});

function ProjectAuditEntry() {
  const { t } = useLingui();
  const { project, entry: id } = Route.useParams();
  const navigate = Route.useNavigate();
  const { opened_from: openedFrom } = Route.useSearch();
  const entry = useQuery(projectAuditEntryQuery(project, id));
  const environmentName = useEnvironmentName(project);
  const environmentId = entry.data?.environment_id;
  const environment = environmentId ? environmentName(environmentId) : undefined;

  return (
    <AuditEntrySheet
      entry={entry.data}
      pending={entry.isPending}
      error={entry.error}
      onRetry={() => void entry.refetch()}
      place={environment ? { label: t`Environment`, value: environment } : undefined}
      onClose={() =>
        void (openedFrom === "overview"
          ? navigate({ to: "/projects/$project", params: { project } })
          : navigate({
              to: "/projects/$project/audit",
              params: { project },
              search: (current) => validateProjectAuditSearch(current),
            }))
      }
    />
  );
}
