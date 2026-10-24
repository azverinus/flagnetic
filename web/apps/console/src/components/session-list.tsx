import { revokeMySession, revokeMySessions, type Session } from "@flagnetic/api";
import { plural } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { LogOutIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";

import { QueryError } from "@/components/query-error";
import { RowAction, withRowActions } from "@/components/row-dialogs";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatDateTime } from "@/i18n/format";
import { deviceName } from "@/lib/device";
import { unwrap } from "@/lib/errors";
import { useListMutation } from "@/lib/mutations";
import { mySessionsQuery } from "@/lib/queries";

/** The sessions with the one this page is signed in to first, the rest newest first. */
export function currentFirst(sessions: readonly Session[]): Session[] {
  return [...sessions].sort((a, b) => Number(b.current) - Number(a.current));
}

/**
 * The columns of a list of sessions: the device, this one marked, its
 * address, and when it signed in, was last seen and ends at the latest.
 */
export function useSessionColumns(): TableColumn<Session>[] {
  const { i18n, t } = useLingui();
  return useMemo<TableColumn<Session>[]>(
    () => [
      {
        id: "device",
        header: t`Device`,
        cell: ({ row }) => (
          <span className="flex flex-wrap items-center gap-2">
            <span title={row.original.user_agent}>{deviceName(i18n, row.original.user_agent)}</span>
            {row.original.current ? (
              <Badge>
                <Trans>This device</Trans>
              </Badge>
            ) : null}
          </span>
        ),
      },
      {
        id: "address",
        header: t`IP address`,
        cell: ({ row }) => (
          <span className="font-mono text-xs">{row.original.client_ip ?? t`Unknown`}</span>
        ),
      },
      {
        id: "created",
        header: t`Signed in`,
        cell: ({ row }) => formatDateTime(i18n, row.original.created_at),
      },
      {
        id: "seen",
        header: t`Last seen`,
        cell: ({ row }) => formatDateTime(i18n, row.original.last_seen_at),
      },
      {
        id: "ends",
        header: t`Ends at the latest`,
        cell: ({ row }) => formatDateTime(i18n, row.original.expires_at),
      },
    ],
    [i18n, t],
  );
}

/**
 * Every device the member is signed in on, this one marked, and signing
 * the others out, one at a time or all at once. This device signs out from
 * the personal menu instead, where signing out is.
 */
export function SessionList() {
  const { i18n, t } = useLingui();
  const sessions = useQuery(mySessionsQuery);
  const [revoking, setRevoking] = useState<Session>();
  const [revokingOthers, setRevokingOthers] = useState(false);
  const rows = useMemo(() => currentFirst(sessions.data ?? []), [sessions.data]);
  const others = rows.filter((s) => !s.current).length;

  const revokeOne = useListMutation(
    (session: Session) => unwrap(revokeMySession({ path: { session: session.id } })),
    mySessionsQuery.queryKey,
    () => {
      setRevoking(undefined);
      toast.success(t`The session is revoked.`);
    },
  );
  const revokeOthers = useListMutation(
    (_: void) => unwrap(revokeMySessions()),
    mySessionsQuery.queryKey,
    () => setRevokingOthers(false),
  );

  const sessionColumns = useSessionColumns();
  const columns = useMemo<TableColumn<Session>[]>(() => {
    return withRowActions(sessionColumns, true, (session) => {
      if (session.current) return null;
      const device = deviceName(i18n, session.user_agent);
      return (
        <RowAction
          label={t`Revoke the session on ${device}`}
          icon={LogOutIcon}
          onClick={() => setRevoking(session)}
        />
      );
    });
  }, [i18n, sessionColumns, t]);

  if (sessions.isError) {
    return <QueryError error={sessions.error} onRetry={() => void sessions.refetch()} />;
  }
  const revokingDevice = revoking ? deviceName(i18n, revoking.user_agent) : "";

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          {sessions.isPending ? null : others === 0 ? (
            <Trans>You are signed in on this device only.</Trans>
          ) : (
            <Trans>A revoked device is signed out at its next request.</Trans>
          )}
        </p>
        <Button variant="outline" disabled={others === 0} onClick={() => setRevokingOthers(true)}>
          <Trans>Revoke all other sessions</Trans>
        </Button>
      </div>
      <Table
        caption={t`The devices you are signed in on`}
        hideCaption
        columns={columns}
        data={rows}
        loading={sessions.isPending}
        getRowId={(session) => session.id}
      />

      <ConfirmDialog
        open={revoking !== undefined}
        onOpenChange={(open) => {
          if (!open) {
            revokeOne.reset();
            setRevoking(undefined);
          }
        }}
        title={t`Revoke the session on ${revokingDevice}`}
        confirmLabel={<Trans>Revoke session</Trans>}
        destructive
        pending={revokeOne.isPending}
        refusal={revokeOne.error}
        onConfirm={() => revoking && revokeOne.mutate(revoking)}
      >
        <p>
          <Trans>That device is signed out at its next request and must sign in again.</Trans>
        </p>
      </ConfirmDialog>
      <ConfirmDialog
        open={revokingOthers}
        onOpenChange={(open) => {
          if (!open) {
            revokeOthers.reset();
            setRevokingOthers(false);
          }
        }}
        title={t`Revoke all other sessions`}
        confirmLabel={t`${plural(others, { one: "Revoke # session", other: "Revoke # sessions" })}`}
        destructive
        pending={revokeOthers.isPending}
        refusal={revokeOthers.error}
        onConfirm={() =>
          revokeOthers.mutate(undefined, {
            onSuccess: ({ revoked }) =>
              toast.success(
                t`${plural(revoked, { one: "Revoked # session.", other: "Revoked # sessions." })}`,
              ),
          })
        }
      >
        <p>
          <Trans>
            Every device but this one is signed out at its next request and must sign in again.
          </Trans>
        </p>
      </ConfirmDialog>
    </div>
  );
}
