import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useContext } from "react";

import { PermissionGate } from "@/components/can";
import { ProjectTokenList } from "@/components/project-token-list";
import { QueryError } from "@/components/query-error";
import { PageHeader } from "@/components/ui/page-header";
import { AccessContext } from "@/lib/access";
import { tokenPolicy } from "@/lib/personal-tokens";
import { meQuery } from "@/lib/queries";
import { useKnownMembers } from "@/lib/use-known-members";

export const Route = createFileRoute("/_signed-in/projects/$project/tokens")({
  staticData: { crumb: msg`Project tokens` },
  component: ProjectTokens,
});

/** The credentials that belong to the project, for its pipelines. */
function ProjectTokens() {
  const { t } = useLingui();
  return (
    <PermissionGate permission="project_token.manage" title={t`Project tokens`}>
      <ProjectTokensScreen />
    </PermissionGate>
  );
}

function ProjectTokensScreen() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  const { data: me } = useSuspenseQuery(meQuery);
  const { roles } = useContext(AccessContext);
  // Who created each token is named from the members the caller can read.
  const { names, failure } = useKnownMembers(project);
  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader title={t`Project tokens`} />
      {failure ? <QueryError error={failure.error} onRetry={failure.retry} /> : null}
      <ProjectTokenList project={project} roles={roles} policy={tokenPolicy(me)} names={names} />
    </div>
  );
}
