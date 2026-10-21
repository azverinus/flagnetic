import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";

import { AuthSettingsForm } from "@/components/auth-settings-form";
import { QueryError } from "@/components/query-error";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { authSettingsQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/admin/auth-settings")({
  staticData: { crumb: msg`Auth settings` },
  component: AuthSettingsPage,
});

/** The instance's rules for sessions, API tokens and passwords. */
function AuthSettingsPage() {
  const { t } = useLingui();
  const settings = useQuery(authSettingsQuery);
  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Auth settings`}
        description={
          <Trans>
            How long sessions last, how long API tokens may live, and how long passwords must be.
            Every replica of the instance applies a change within a second.
          </Trans>
        }
      />
      {settings.data ? (
        <AuthSettingsForm settings={settings.data} />
      ) : settings.isError ? (
        <QueryError error={settings.error} onRetry={() => void settings.refetch()} />
      ) : (
        <div className="flex max-w-2xl flex-col gap-5" aria-busy>
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      )}
    </div>
  );
}
