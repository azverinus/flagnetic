import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, isRedirect, Link, redirect } from "@tanstack/react-router";
import { LayersIcon } from "lucide-react";

import { ProjectsTable } from "@/components/projects-table";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { lastProject } from "@/lib/preferences";
import { meQuery, projectsQuery } from "@/lib/queries";

type ProjectsSearch = {
  /** Set to list every project instead of reopening the last one. */
  all?: true;
};

export const Route = createFileRoute("/_signed-in/")({
  validateSearch: (search: Record<string, unknown>): ProjectsSearch =>
    search.all === true || search.all === "true" ? { all: true } : {},
  beforeLoad: async ({ context, search }) => {
    if (search.all) return;
    const last = lastProject();
    if (!last) return;
    try {
      // Reopened only while the member still holds a grant in it.
      const projects = await context.queryClient.ensureQueryData(projectsQuery);
      if (projects.some((project) => project.key === last)) {
        throw redirect({ to: "/projects/$project", params: { project: last } });
      }
    } catch (error) {
      if (isRedirect(error)) throw error;
      // The list renders its own error below.
    }
  },
  staticData: { crumb: msg`Projects` },
  component: Projects,
});

/** The projects the member holds a grant in, every project for an instance admin. */
function Projects() {
  const { t } = useLingui();
  const { data: me } = useSuspenseQuery(meQuery);
  const projects = useQuery(projectsQuery);
  const instanceAdmin = me.member?.instance_admin === true;
  const name = me.member?.display_name || me.member?.email || "";

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Projects`}
        description={<Trans>Welcome, {name}. Choose a project to work in.</Trans>}
        actions={
          instanceAdmin ? (
            <Button asChild>
              <Link to="/admin/projects">
                <Trans>New project</Trans>
              </Link>
            </Button>
          ) : null
        }
      />
      {projects.isError ? (
        <QueryError error={projects.error} onRetry={() => void projects.refetch()} />
      ) : projects.data?.length === 0 ? (
        <EmptyState
          icon={<LayersIcon />}
          title={<Trans>No projects yet</Trans>}
          description={
            instanceAdmin ? (
              <Trans>Create the first project to start declaring what it serves.</Trans>
            ) : (
              <Trans>
                You hold no grant in any project yet. Ask a project owner or an instance admin.
              </Trans>
            )
          }
        />
      ) : (
        <ProjectsTable
          caption={t`Projects`}
          projects={projects.data ?? []}
          loading={projects.isPending}
        />
      )}
    </div>
  );
}
