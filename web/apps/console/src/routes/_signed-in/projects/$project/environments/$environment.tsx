import { Trans, useLingui } from "@lingui/react/macro";
import { Page } from "@/components/ui/page";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";

import { DownloadRuleset } from "@/components/download-ruleset";
import { EnvironmentProtection } from "@/components/environment-protection";
import { ProtectedMark } from "@/components/protected-mark";
import { QueryError } from "@/components/query-error";
import { SdkInventory } from "@/components/sdk-inventory";
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
      <Page>
        <QueryError error={environment.error} onRetry={() => void environment.refetch()} />
      </Page>
    );
  }
  if (environment.isPending) {
    return (
      <Page>
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-40 w-full" />
      </Page>
    );
  }

  return (
    <Page>
      <PageHeader
        title={environment.data.name}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <code className="font-mono text-xs">{environment.data.key}</code>
            {environment.data.protected ? <ProtectedMark /> : null}
          </span>
        }
        actions={<DownloadRuleset project={project} environment={key} />}
      />
      <EnvironmentProtection project={project} environment={environment.data} />
      <Tabs
        label={t`About ${key}`}
        tabs={[
          {
            value: "sdk-keys",
            label: <Trans>SDK keys</Trans>,
            content: <SdkKeys project={project} environment={key} />,
          },
          {
            value: "sdks",
            label: <Trans>SDKs</Trans>,
            content: <SdkInventory project={project} environment={key} />,
          },
        ]}
      />
    </Page>
  );
}

function EnvironmentNotFound({ project }: { project: string }) {
  return (
    <Page measure="form">
      <h1 className="text-2xl/7 font-semibold">
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
    </Page>
  );
}
