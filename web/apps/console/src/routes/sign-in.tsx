import { Trans } from "@lingui/react/macro";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, isRedirect, redirect, useRouter } from "@tanstack/react-router";

import { SignInForm } from "@/components/sign-in-form";
import { SignedOutCard } from "@/components/signed-out-card";
import { meQuery } from "@/lib/queries";
import { safeReturnTo } from "@/lib/return-to";
import { validateSignInSearch } from "@/lib/signed-in";

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
  const { returnTo, ended } = Route.useSearch();
  const queryClient = useQueryClient();
  const router = useRouter();
  return (
    <SignedOutCard className="max-w-sm">
      <h1 className="text-xl font-semibold">
        <Trans>Sign in</Trans>
      </h1>
      <SignInForm
        ended={ended}
        onSignedIn={(me) => {
          queryClient.setQueryData(meQuery.queryKey, me);
          void router.navigate({ href: safeReturnTo(returnTo), replace: true });
        }}
      />
    </SignedOutCard>
  );
}
