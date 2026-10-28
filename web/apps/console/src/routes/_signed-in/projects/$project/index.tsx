import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useId } from "react";

import { RecentActivity } from "@/components/project-audit";
import { QueryError } from "@/components/query-error";
import { EnvironmentName } from "@/components/protected-mark";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { environmentsQuery, projectQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/projects/$project/")({
  component: Overview,
});

/**
 * The project at a glance: what it is called, where it serves from and
 * what changed in it last.
 */
function Overview() {
  const { t } = useLingui();
  const environmentsHeading = useId();
  const { project: key } = Route.useParams();
  const { data: project } = useSuspenseQuery(projectQuery(key));
  const environments = useQuery(environmentsQuery(key));

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={project.name}
        description={
          project.description || (
            <Trans>
              Key <code className="font-mono">{key}</code>
            </Trans>
          )
        }
      />
      <section aria-labelledby={environmentsHeading} className="flex flex-col gap-3">
        <div className="flex items-center justify-between gap-4">
          <h2 id={environmentsHeading} className="text-base font-semibold">
            <Trans>Environments</Trans>
          </h2>
          <Link
            to="/projects/$project/environments"
            params={{ project: key }}
            className="text-sm text-muted-foreground hover:text-foreground hover:underline"
          >
            <Trans>View all environments</Trans>
          </Link>
        </div>
        {environments.isError ? (
          <QueryError error={environments.error} onRetry={() => void environments.refetch()} />
        ) : environments.isPending ? (
          <Skeleton className="h-16 w-full" />
        ) : (
          <ul aria-label={t`Environments`} className="flex flex-wrap gap-3">
            {environments.data.map((environment) => (
              <li
                key={environment.id}
                className="flex min-w-40 flex-col gap-0.5 rounded-md border bg-card px-4 py-3"
              >
                <span className="font-medium">
                  <EnvironmentName environment={environment} />
                </span>
                <code className="font-mono text-xs text-muted-foreground">{environment.key}</code>
              </li>
            ))}
          </ul>
        )}
      </section>
      <RecentActivity project={key} />
    </div>
  );
}
