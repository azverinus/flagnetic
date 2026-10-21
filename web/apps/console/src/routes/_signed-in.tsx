import { i18n } from "@lingui/core";
import { createFileRoute, Outlet } from "@tanstack/react-router";

import { AppShell } from "@/components/app-shell";
import { followMemberLocale } from "@/lib/member-locale";
import { requireSignedIn } from "@/lib/signed-in";

/**
 * Every page behind sign-in renders in the shell, after the guard, in the
 * language the member chose: the page before sign-in could only guess it.
 */
export const Route = createFileRoute("/_signed-in")({
  beforeLoad: async ({ context, location }) => {
    const me = await requireSignedIn(context.queryClient, location);
    await followMemberLocale(i18n, me);
    return me;
  },
  component: SignedIn,
});

function SignedIn() {
  return (
    <AppShell>
      <Outlet />
    </AppShell>
  );
}
