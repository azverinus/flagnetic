import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { createFileRoute, Outlet } from "@tanstack/react-router";

import { ProjectAuditPanel, useProjectAuditFilters } from "@/components/audit-filters";
import { AuditLog } from "@/components/audit-log";
import { ProjectAuditEntryLink } from "@/components/project-audit";
import { Page } from "@/components/ui/page";
import { PageHeader } from "@/components/ui/page-header";
import { validateProjectAuditSearch } from "@/lib/audit";

export const Route = createFileRoute("/_signed-in/projects/$project/audit")({
  validateSearch: validateProjectAuditSearch,
  staticData: { crumb: msg`Audit log`, panel: ProjectAuditPanel },
  component: ProjectAudit,
});

/** Every change made in the project, by whom and when. */
function ProjectAudit() {
  const { t } = useLingui();
  // A route's search also carries what its children read, such as an open
  // entry's way back; the filters take the log's own alone, so opening an
  // entry does not read the page again or count as a filter.
  const filters = useProjectAuditFilters();
  return (
    <Page>
      <PageHeader
        title={t`Audit log`}
        description={
          <Trans>
            Every change made in this project, newest first, with its values before and after.
          </Trans>
        }
      />
      <AuditLog filters={filters} EntryLink={ProjectAuditEntryLink} />
      <Outlet />
    </Page>
  );
}
