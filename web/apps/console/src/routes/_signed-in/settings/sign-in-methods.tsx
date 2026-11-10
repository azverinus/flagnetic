import { msg } from "@lingui/core/macro";
import { createFileRoute } from "@tanstack/react-router";

import { SignInMethods } from "@/components/sign-in-methods";
import { validateLinkErrorSearch } from "@/lib/signed-in";

export const Route = createFileRoute("/_signed-in/settings/sign-in-methods")({
  validateSearch: validateLinkErrorSearch,
  staticData: { crumb: msg`Sign-in methods` },
  component: SignInMethodsPage,
});

function SignInMethodsPage() {
  const { error } = Route.useSearch();
  return <SignInMethods error={error} />;
}
