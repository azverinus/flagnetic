import { createFileRoute, redirect } from "@tanstack/react-router";

import { validateLinkErrorSearch } from "@/lib/signed-in";

/**
 * The personal settings open on the profile. A linking that failed
 * returns the browser here with the code of its error, which belongs to
 * the sign-in methods, so that is where it goes.
 */
export const Route = createFileRoute("/_signed-in/settings/")({
  validateSearch: validateLinkErrorSearch,
  beforeLoad: ({ search }) => {
    if (search.error) {
      throw redirect({ to: "/settings/sign-in-methods", search, replace: true });
    }
    throw redirect({ to: "/settings/profile", replace: true });
  },
});
