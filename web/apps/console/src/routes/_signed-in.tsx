import { createFileRoute, Outlet } from "@tanstack/react-router";

import { AppShell } from "@/components/app-shell";
import { requireSignedIn } from "@/lib/signed-in";

/** Every page behind sign-in renders in the shell, after the guard. */
export const Route = createFileRoute("/_signed-in")({
  beforeLoad: ({ context, location }) => requireSignedIn(context.queryClient, location),
  component: SignedIn,
});

function SignedIn() {
  return (
    <AppShell>
      <Outlet />
    </AppShell>
  );
}
