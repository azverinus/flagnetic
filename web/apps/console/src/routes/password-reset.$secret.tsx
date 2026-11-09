import { useLingui } from "@lingui/react/macro";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useRouter } from "@tanstack/react-router";
import { toast } from "sonner";

import { PasswordResetCompletion } from "@/components/password-reset-completion";
import { SignedOutCard } from "@/components/signed-out-card";
import { meQuery } from "@/lib/queries";

/**
 * The page a password reset's one-time link opens, before anyone is signed
 * in: the member chooses a new password and is signed in with it.
 */
export const Route = createFileRoute("/password-reset/$secret")({
  component: PasswordResetPage,
});

function PasswordResetPage() {
  const { t } = useLingui();
  const { secret } = Route.useParams();
  const queryClient = useQueryClient();
  const router = useRouter();
  return (
    <SignedOutCard className="max-w-md">
      <PasswordResetCompletion
        secret={secret}
        onReset={(me) => {
          // Whoever this browser was signed in as before, the member is
          // signed in now; nothing read for another stays.
          queryClient.clear();
          queryClient.setQueryData(meQuery.queryKey, me);
          toast.success(t`Your new password is set. Every other session of yours is signed out.`);
          void router.navigate({ to: "/", replace: true });
        }}
      />
    </SignedOutCard>
  );
}
