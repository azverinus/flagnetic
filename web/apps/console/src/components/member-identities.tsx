import { type LinkedIdentity, type Member, unlinkMemberIdentity } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { IdentityList, UnlinkIdentityDialog } from "@/components/identity-list";
import { QueryError } from "@/components/query-error";
import { formatDateTime } from "@/i18n/format";
import { unwrap } from "@/lib/errors";
import { memberName } from "@/lib/members";
import { memberIdentitiesQuery } from "@/lib/queries";

/**
 * The identities a member linked at auth providers and whether they hold a
 * password, for an instance admin fixing a wrong link: unlinking one is
 * refused while it is the member's last usable way in, except for a
 * deactivated member, who signs in nowhere.
 */
export function MemberIdentities({ member }: { member: Member }) {
  const { i18n, t } = useLingui();
  const methods = useQuery(memberIdentitiesQuery(member.id));
  const [unlinking, setUnlinking] = useState<LinkedIdentity>();
  const name = memberName(member);

  if (methods.isError) {
    return <QueryError error={methods.error} onRetry={() => void methods.refetch()} />;
  }
  const password = methods.data?.password;
  const setAt = password?.set_at ? formatDateTime(i18n, password.set_at) : "";
  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        {password === undefined ? null : password.set && password.set_at ? (
          <Trans>
            {name} holds a password, set on <time dateTime={password.set_at}>{setAt}</time>.
          </Trans>
        ) : password.set ? (
          <Trans>{name} holds a password.</Trans>
        ) : (
          <Trans>{name} holds no password.</Trans>
        )}
      </p>
      <IdentityList
        identities={methods.data?.identities}
        caption={t`The identities ${name} linked`}
        empty={<Trans>{name} has linked no identity.</Trans>}
        onUnlink={setUnlinking}
      />
      <UnlinkIdentityDialog
        identity={unlinking}
        onClose={() => setUnlinking(undefined)}
        unlink={(identity) =>
          unwrap(unlinkMemberIdentity({ path: { member: member.id, identity: identity.id } }))
        }
        list={memberIdentitiesQuery(member.id).queryKey}
      >
        {(identity) => {
          const provider = identity.provider_name;
          return (
            <p>
              <Trans>
                {name} will no longer be able to sign in through {provider} with this account. The
                member and everything they hold stays, and they can link it again.
              </Trans>
            </p>
          );
        }}
      </UnlinkIdentityDialog>
    </div>
  );
}
