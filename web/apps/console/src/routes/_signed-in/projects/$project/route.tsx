import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Outlet } from "@tanstack/react-router";
import { useEffect } from "react";

import { projectNameCrumb } from "@/components/breadcrumb";
import { QueryError } from "@/components/query-error";
import { Skeleton } from "@/components/ui/skeleton";
import { AccessProvider } from "@/components/access-provider";
import { ProjectWebhookBanner } from "@/components/webhook-disabled-banner";
import { ApiError } from "@/lib/errors";
import { rememberLastProject } from "@/lib/preferences";
import { problemMessage } from "@/lib/problem-messages";
import { projectMeQuery, projectQuery, rolesQuery } from "@/lib/queries";

/**
 * One project: every screen under it renders inside the shell with what
 * the caller may do there. An unknown project and one the caller holds
 * no grant in are told apart, both inside the shell, so the switcher is
 * still at hand to choose another.
 */
export const Route = createFileRoute("/_signed-in/projects/$project")({
  loader: async ({ context: { queryClient }, params: { project } }) => {
    // Read ahead of the first render; a refusal is rendered by the page.
    await Promise.allSettled([
      queryClient.ensureQueryData(projectQuery(project)),
      queryClient.ensureQueryData(projectMeQuery(project)),
      queryClient.ensureQueryData(rolesQuery),
    ]);
  },
  staticData: { crumb: projectNameCrumb },
  component: ProjectLayout,
});

function ProjectLayout() {
  const { project: key } = Route.useParams();
  const project = useQuery(projectQuery(key));
  const me = useQuery(projectMeQuery(key));
  // Once read, the project and the caller's permissions stay in use while
  // a refresh of them fails; only a first read that failed is shown.
  const opened = project.data !== undefined && me.data !== undefined;

  useEffect(() => {
    if (opened) rememberLastProject(key);
  }, [opened, key]);

  // The project's own read decides first: an unknown project is not found
  // whatever the caller holds.
  const error =
    (project.data === undefined ? project.error : null) ??
    (me.data === undefined ? me.error : null);
  if (error) {
    if (error instanceof ApiError && error.code === "not_found") return <ProjectNotFound />;
    if (error instanceof ApiError && error.status === 403)
      return <ProjectForbidden error={error} />;
    return (
      <div className="mx-auto max-w-5xl p-6">
        <QueryError
          error={error}
          onRetry={() => {
            if (project.isError) void project.refetch();
            if (me.isError) void me.refetch();
          }}
        />
      </div>
    );
  }
  if (!opened) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-4 p-6" aria-busy>
        <Skeleton className="h-7 w-48" />
        <Skeleton className="h-4 w-72" />
      </div>
    );
  }
  return (
    <AccessProvider project={key}>
      <ProjectWebhookBanner project={key} />
      <Outlet />
    </AccessProvider>
  );
}

function ProjectNotFound() {
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>Project not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>
          No project has this key. It may be mistyped, or the link may point to another instance.
        </Trans>
      </p>
    </div>
  );
}

function ProjectForbidden({ error }: { error: ApiError }) {
  const { i18n } = useLingui();
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>You cannot open this project</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">{problemMessage(i18n, error)}</p>
      <p className="text-sm text-muted-foreground">
        <Trans>A project owner or an instance admin can grant you a role in it.</Trans>
      </p>
    </div>
  );
}
