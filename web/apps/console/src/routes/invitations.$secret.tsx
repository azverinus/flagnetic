import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useRouter } from "@tanstack/react-router";

import { InvitationAcceptance } from "@/components/invitation-acceptance";
import { SignedOutCard } from "@/components/signed-out-card";
import { meQuery } from "@/lib/queries";

/**
 * The page an invitation's one-time link opens, before anyone is signed
 * in: the invited person sets their first password and is signed in.
 */
export const Route = createFileRoute("/invitations/$secret")({
  component: InvitationPage,
});

function InvitationPage() {
  const { secret } = Route.useParams();
  const queryClient = useQueryClient();
  const router = useRouter();
  return (
    <SignedOutCard className="max-w-md">
      <InvitationAcceptance
        secret={secret}
        onAccepted={(me) => {
          // Whoever this browser was signed in as before, the new
          // member is signed in now; nothing read for another stays.
          queryClient.clear();
          queryClient.setQueryData(meQuery.queryKey, me);
          void router.navigate({ to: "/", replace: true });
        }}
      />
    </SignedOutCard>
  );
}
