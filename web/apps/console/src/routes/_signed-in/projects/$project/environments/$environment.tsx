import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";

import { QueryError } from "@/components/query-error";
import { SdkKeys } from "@/components/sdk-keys";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs } from "@/components/ui/tabs";
import { ApiError } from "@/lib/errors";
import { environmentQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/projects/$project/environments/$environment")({
  staticData: { crumb: { param: "environment" } },
  validateSearch: (search: Record<string, unknown>): { tab?: string } =>
    typeof search.tab === "string" ? { tab: search.tab } : {},
  component: EnvironmentPage,
});

/** One environment: what a service needs to be served from it, in tabs. */
function EnvironmentPage() {
  const { t } = useLingui();
  const { project, environment: key } = Route.useParams();
  const environment = useQuery(environmentQuery(project, key));

  if (environment.isError) {
    if (environment.error instanceof ApiError && environment.error.status === 404) {
      return <EnvironmentNotFound project={project} />;
    }
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <QueryError error={environment.error} onRetry={() => void environment.refetch()} />
      </div>
    );
  }
  if (environment.isPending) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={environment.data.name}
        description={<code className="font-mono text-xs">{environment.data.key}</code>}
      />
      <Tabs
        label={t`About ${key}`}
        tabs={[
          {
            value: "sdk-keys",
            label: <Trans>SDK keys</Trans>,
            content: <SdkKeys project={project} environment={key} />,
          },
        ]}
      />
    </div>
  );
}

function EnvironmentNotFound({ project }: { project: string }) {
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>Environment not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>The project has no environment with this key. It may have been deleted.</Trans>
      </p>
      <Button asChild variant="outline">
        <Link to="/projects/$project/environments" params={{ project }}>
          <Trans>Go to the environments</Trans>
        </Link>
      </Button>
    </div>
  );
}
