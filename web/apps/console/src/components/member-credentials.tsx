import { type Member, revokeMemberSessions, revokeMemberToken } from "@flagnetic/api";
import { plural } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { toast } from "sonner";

import { QueryError } from "@/components/query-error";
import { currentFirst, useSessionColumns } from "@/components/session-list";
import { useRevocableTokens } from "@/components/token-list";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Table } from "@/components/ui/table";
import { unwrap } from "@/lib/errors";
import { memberName } from "@/lib/members";
import { useListMutation } from "@/lib/mutations";
import { memberSessionsQuery, memberTokensQuery } from "@/lib/queries";

/**
 * The devices a member is signed in on, for an instance admin answering an
 * incident, and signing the member out of every one of them at once. On
 * their own page that includes the device they are using.
 */
export function MemberSessions({ member, self }: { member: Member; self: boolean }) {
  const { t } = useLingui();
  const sessions = useQuery(memberSessionsQuery(member.id));
  const [revoking, setRevoking] = useState(false);
  const rows = useMemo(() => currentFirst(sessions.data ?? []), [sessions.data]);
  const live = rows.length;
  const columns = useSessionColumns();
  const name = memberName(member);
  const revoke = useListMutation(
    (_: void) => unwrap(revokeMemberSessions({ path: { member: member.id } })),
    memberSessionsQuery(member.id).queryKey,
    () => setRevoking(false),
  );

  if (sessions.isError) {
    return <QueryError error={sessions.error} onRetry={() => void sessions.refetch()} />;
  }
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          <Trans>A revoked device is signed out at its next request.</Trans>
        </p>
        <Button variant="outline" disabled={live === 0} onClick={() => setRevoking(true)}>
          <Trans>Revoke all sessions</Trans>
        </Button>
      </div>
      <Table
        caption={t`The devices ${name} is signed in on`}
        hideCaption
        columns={columns}
        data={rows}
        loading={sessions.isPending}
        empty={<Trans>{name} is not signed in anywhere.</Trans>}
        getRowId={(session) => session.id}
      />
      <ConfirmDialog
        open={revoking}
        onOpenChange={(open) => {
          if (!open) {
            revoke.reset();
            setRevoking(false);
          }
        }}
        title={self ? t`Sign yourself out everywhere` : t`Sign ${name} out everywhere`}
        confirmLabel={t`${plural(live, { one: "Revoke # session", other: "Revoke # sessions" })}`}
        destructive
        pending={revoke.isPending}
        refusal={revoke.error}
        onConfirm={() =>
          revoke.mutate(undefined, {
            onSuccess: ({ revoked }) =>
              toast.success(
                t`${plural(revoked, { one: "Revoked # session.", other: "Revoked # sessions." })}`,
              ),
          })
        }
      >
        <p>
          {self ? (
            <Trans>
              Every device you are signed in on is signed out, this one included, and you will have
              to sign in again. Your API tokens keep working.
            </Trans>
          ) : (
            <Trans>
              Every device {name} is signed in on is signed out at its next request. Their API
              tokens keep working.
            </Trans>
          )}
        </p>
      </ConfirmDialog>
    </div>
  );
}

/**
 * A member's personal access tokens, revoked and expired ones included,
 * for an instance admin answering an incident, and revoking any of them.
 */
export function MemberTokens({ member }: { member: Member }) {
  const { t } = useLingui();
  const tokens = useQuery(memberTokensQuery(member.id));
  const name = memberName(member);
  const { columns, revokeDialog } = useRevocableTokens(
    tokens.data,
    (token) => unwrap(revokeMemberToken({ path: { member: member.id, token: token.id } })),
    memberTokensQuery(member.id).queryKey,
  );

  if (tokens.isError) {
    return <QueryError error={tokens.error} onRetry={() => void tokens.refetch()} />;
  }
  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        <Trans>
          Only {name} creates their tokens. Project tokens they created belong to their projects,
          not to them.
        </Trans>
      </p>
      <Table
        caption={t`The API tokens of ${name}`}
        hideCaption
        columns={columns}
        data={tokens.data ?? []}
        loading={tokens.isPending}
        empty={<Trans>{name} has no API token.</Trans>}
        getRowId={(token) => token.id}
      />
      {revokeDialog}
    </div>
  );
}
