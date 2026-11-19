import { Trans } from "@lingui/react/macro";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, isRedirect, redirect, useRouter } from "@tanstack/react-router";

import { SignInForm } from "@/components/sign-in-form";
import { SignedOutCard } from "@/components/signed-out-card";
import { meQuery } from "@/lib/queries";
import { safeReturnTo } from "@/lib/return-to";
import { announceSessionChange, validateSignInSearch } from "@/lib/signed-in";
import { PageTitle } from "@/components/ui/page-header";

export const Route = createFileRoute("/sign-in")({
  validateSearch: validateSignInSearch,
  beforeLoad: async ({ context, search }) => {
    // A member already signed in has nothing to do here.
    try {
      await context.queryClient.ensureQueryData(meQuery);
      throw redirect({ href: safeReturnTo(search.returnTo) });
    } catch (error) {
      if (isRedirect(error)) throw error;
    }
  },
  component: SignInPage,
});

function SignInPage() {
  const { returnTo, ended, error, provider } = Route.useSearch();
  const queryClient = useQueryClient();
  const router = useRouter();
  return (
    <SignedOutCard>
      <PageTitle>
        <Trans>Sign in</Trans>
      </PageTitle>
      <SignInForm
        ended={ended}
        returnTo={returnTo}
        error={error}
        errorProvider={provider}
        onSignedIn={(me) => {
          queryClient.setQueryData(meQuery.queryKey, me);
          announceSessionChange("signed-in");
          void router.navigate({ href: safeReturnTo(returnTo), replace: true });
        }}
      />
    </SignedOutCard>
  );
}
