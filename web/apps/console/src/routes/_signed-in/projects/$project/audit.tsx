import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Outlet } from "@tanstack/react-router";
import { useCallback, useMemo } from "react";

import { AuditLog, type AuditLogProps } from "@/components/audit-log";
import { ProjectAuditEntryLink, useEnvironmentPlace } from "@/components/project-audit";
import { PageHeader } from "@/components/ui/page-header";
import { environmentLabel } from "@/lib/environments";
import { type AuditSearch, validateProjectAuditSearch } from "@/lib/audit";
import { environmentsQuery, meQuery, membersQuery, projectMembersQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/projects/$project/audit")({
  validateSearch: validateProjectAuditSearch,
  staticData: { crumb: msg`Audit log` },
  component: ProjectAudit,
});

/** Every change made in the project, by whom and when. */
function ProjectAudit() {
  const { i18n, t } = useLingui();
  const { project } = Route.useParams();
  const address = Route.useSearch();
  // A route's search also carries what its children read, such as an
  // open entry's way back; the listing, its cache key and the filter form
  // are built from the log's own filters alone, so opening an entry does
  // not read the page again or count as a filter.
  const search = useMemo(() => validateProjectAuditSearch(address), [address]);
  const navigate = Route.useNavigate();
  const { data: me } = useSuspenseQuery(meQuery);
  const members = useQuery(projectMembersQuery(project));
  // An instance admin acts in every project without a grant; reading
  // every member, which only they may, names them all in the filter.
  const instanceMembers = useQuery({
    ...membersQuery,
    enabled: me.member?.instance_admin === true,
  });
  const environments = useQuery(environmentsQuery(project));

  const onSearch = useCallback((next: AuditSearch) => void navigate({ search: next }), [navigate]);
  const actors = useMemo(
    () => [
      ...(members.data ?? []).map((member) => ({
        value: member.member,
        label: member.display_name || member.email,
      })),
      ...(instanceMembers.data ?? []).map((member) => ({
        value: member.id,
        label: member.display_name || member.email,
      })),
    ],
    [members.data, instanceMembers.data],
  );
  const environmentPlace = useEnvironmentPlace(project);
  const place = useMemo<AuditLogProps["place"]>(
    () => ({
      ...environmentPlace,
      filter: "environment",
      options: (environments.data ?? []).map((environment) => ({
        value: environment.id,
        label: environmentLabel(i18n, environment),
      })),
    }),
    [environmentPlace, environments.data, i18n],
  );

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Audit log`}
        description={
          <Trans>
            Every change made in this project, newest first, with its values before and after.
          </Trans>
        }
      />
      <AuditLog
        scope={{ project }}
        search={search}
        onSearch={onSearch}
        EntryLink={ProjectAuditEntryLink}
        actors={actors}
        place={place}
      />
      <Outlet />
    </div>
  );
}
