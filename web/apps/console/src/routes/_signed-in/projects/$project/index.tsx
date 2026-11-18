import { Trans, useLingui } from "@lingui/react/macro";
import { Page } from "@/components/ui/page";
import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useId } from "react";

import { RecentActivity } from "@/components/project-audit";
import { NeedsAttention } from "@/components/needs-attention";
import { QueryError } from "@/components/query-error";
import { EnvironmentName } from "@/components/protected-mark";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { environmentsQuery, projectQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/projects/$project/")({
  component: Overview,
});

/**
 * The project at a glance: where it serves from, what waits on the member
 * and what changed in it last.
 */
function Overview() {
  const { t } = useLingui();
  const environmentsHeading = useId();
  const { project: key } = Route.useParams();
  const { data: project } = useSuspenseQuery(projectQuery(key));
  const environments = useQuery(environmentsQuery(key));

  return (
    <Page>
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
      <div className="grid items-start gap-5 xl:grid-cols-2">
        <Card aria-labelledby={environmentsHeading}>
          <CardHeader
            title={<Trans>Environments</Trans>}
            titleId={environmentsHeading}
            actions={
              <Link
                to="/projects/$project/environments"
                params={{ project: key }}
                className="text-sm text-muted-foreground hover:text-foreground hover:underline"
              >
                <Trans>View all environments</Trans>
              </Link>
            }
          />
          <CardContent>
            {environments.isError ? (
              <QueryError error={environments.error} onRetry={() => void environments.refetch()} />
            ) : environments.isPending ? (
              <Skeleton className="h-16 w-full" />
            ) : (
              <ul aria-label={t`Environments`} className="-mx-3 flex flex-col">
                {environments.data.map((environment) => (
                  <li key={environment.id}>
                    <Link
                      to="/projects/$project/environments/$environment"
                      params={{ project: key, environment: environment.key }}
                      className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 rounded-md px-3 py-2 text-sm hover:bg-accent"
                    >
                      <span className="font-medium">
                        <EnvironmentName environment={environment} />
                      </span>
                      <code className="font-mono text-xs text-muted-foreground">
                        {environment.key}
                      </code>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
        <NeedsAttention project={key} />
      </div>
      <RecentActivity project={key} />
    </Page>
  );
}
