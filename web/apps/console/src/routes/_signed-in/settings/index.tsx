import { createFileRoute, redirect } from "@tanstack/react-router";

/** The personal settings open on the profile. */
export const Route = createFileRoute("/_signed-in/settings/")({
  beforeLoad: () => {
    throw redirect({ to: "/settings/profile", replace: true });
  },
});
