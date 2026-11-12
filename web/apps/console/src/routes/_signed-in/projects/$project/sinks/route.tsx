import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import { createFileRoute, Outlet } from "@tanstack/react-router";

import { PermissionGate } from "@/components/can";

/** A project's sinks: the list, a new sink and one sink under it. */
export const Route = createFileRoute("/_signed-in/projects/$project/sinks")({
  staticData: { crumb: msg`Sinks` },
  component: Sinks,
});

function Sinks() {
  const { t } = useLingui();
  return (
    <PermissionGate permission="sink.manage" title={t`Sinks`}>
      <Outlet />
    </PermissionGate>
  );
}
