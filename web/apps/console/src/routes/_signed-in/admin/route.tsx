import { useLingui } from "@lingui/react/macro";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Outlet } from "@tanstack/react-router";

import { RefusedScreen } from "@/components/can";
import { meQuery } from "@/lib/queries";

/**
 * The instance's screens, for instance admins. Anyone else reaching one
 * by its address is told so inside the shell, without a request the
 * server would refuse.
 */
export const Route = createFileRoute("/_signed-in/admin")({
  component: Instance,
});

function Instance() {
  const { t } = useLingui();
  const { data: me } = useSuspenseQuery(meQuery);
  if (!me.member?.instance_admin) {
    return (
      <RefusedScreen
        title={t`Instance administration`}
        reason={t`Only an instance admin can open it.`}
      />
    );
  }
  return <Outlet />;
}
