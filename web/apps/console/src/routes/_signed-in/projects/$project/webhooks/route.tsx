import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import { createFileRoute, Outlet } from "@tanstack/react-router";

import { PermissionGate } from "@/components/can";
import { projectSettingsArea } from "@/components/area-screens";
import { ProjectSettingsPanel } from "@/components/settings-panels";

/** A project's webhooks: the list, and one subscription under it. */
export const Route = createFileRoute("/_signed-in/projects/$project/webhooks")({
  staticData: {
    crumb: msg`Webhooks`,
    area: projectSettingsArea,
    panel: ProjectSettingsPanel,
  },
  component: Webhooks,
});

function Webhooks() {
  const { t } = useLingui();
  return (
    <PermissionGate permission="webhook.manage" title={t`Webhooks`}>
      <Outlet />
    </PermissionGate>
  );
}
