import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import { createFileRoute, Outlet } from "@tanstack/react-router";

import { PersonalSettingsPanel } from "@/components/settings-panels";
import { Page } from "@/components/ui/page";
import { PageHeader } from "@/components/ui/page-header";

/** The member's own account: who they are, how they sign in, and their tokens. */
export const Route = createFileRoute("/_signed-in/settings")({
  staticData: { crumb: msg`Personal settings`, panel: PersonalSettingsPanel },
  component: PersonalSettings,
});

function PersonalSettings() {
  const { t } = useLingui();
  return (
    <Page>
      <PageHeader title={t`Personal settings`} />
      <Outlet />
    </Page>
  );
}
