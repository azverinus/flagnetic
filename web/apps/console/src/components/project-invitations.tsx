import {
  inviteProjectMember,
  type MemberInvitation,
  type ProjectInvitation,
  type ProjectInvitationCreate,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { MailPlusIcon } from "lucide-react";
import { useContext, useMemo, useState } from "react";

import { InviteForm } from "@/components/invite-form";
import { QueryError } from "@/components/query-error";
import { RowAction, withRowActions } from "@/components/row-dialogs";
import { Badge } from "@/components/ui/badge";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog } from "@/components/ui/dialog";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatDateTime } from "@/i18n/format";
import { AccessContext } from "@/lib/access";
import { roleName } from "@/lib/access-names";
import { unwrap } from "@/lib/errors";
import { memberName } from "@/lib/members";
import { forgetsSecret } from "@/lib/mutations";
import { grantableRoles, invitationExpired, madeBy } from "@/lib/project-members";
import { projectInvitationsQuery } from "@/lib/queries";
import { useNowPassing } from "@/lib/use-now";

/**
 * Inviting to a project, or inviting a member listed as invited there
 * again: the answer carries the invitation, mailed or with its one-time
 * link, which lives only in the dialog that shows it. The mutation is
 * dropped once it answers, and with no cache time the mutation cache
 * forgets it, answer included.
 */
function useProjectInvite(project: string, onInvited: (answer: MemberInvitation) => void) {
  const queryClient = useQueryClient();
  const invite = useMutation({
    mutationFn: (body: ProjectInvitationCreate) =>
      unwrap(inviteProjectMember({ path: { project }, body })),
    ...forgetsSecret,
    meta: { toast: false },
    onSuccess: async (answer) => {
      await queryClient.invalidateQueries({ queryKey: projectInvitationsQuery(project).queryKey });
      invite.reset();
      onInvited(answer);
    },
  });
  return invite;
}

export type ProjectInviteDialogProps = {
  project: string;
  open: boolean;
  onClose: () => void;
  onInvited: (answer: MemberInvitation) => void;
};

/**
 * Inviting someone the instance does not know yet to the project, with one
 * of the roles the caller may grant here, which accepting grants them.
 */
export function ProjectInviteDialog({
  project,
  open,
  onClose,
  onInvited,
}: ProjectInviteDialogProps) {
  const { t } = useLingui();
  const { roles, permissions } = useContext(AccessContext);
  const offered = useMemo(() => grantableRoles(roles, permissions), [roles, permissions]);
  const invite = useProjectInvite(project, onInvited);
  const close = () => {
    invite.reset();
    onClose();
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => !next && close()}
      title={t`Invite to the project`}
      description={
        <Trans>
          Someone without an account here joins the instance through the invitation, and accepting
          it grants them the role. A member of the instance is granted a role instead.
        </Trans>
      }
    >
      {open ? (
        <InviteForm
          roles={offered}
          pending={invite.isPending}
          refusal={invite.error}
          onCancel={close}
          onSubmit={({ email, display_name, role }) => invite.mutate({ email, display_name, role })}
        />
      ) : null}
    </Dialog>
  );
}

export type InvitedMembersProps = {
  project: string;
  /** The names of the members the caller can read, for saying who invited. */
  names: ReadonlyMap<string, string>;
  /** Whether the names are still being read, so nobody is named wrongly meanwhile. */
  namesPending: boolean;
  /** Called with the answer of inviting a listed member again. */
  onReissued: (answer: MemberInvitation) => void;
};

/**
 * The members invited to the project who have not accepted yet, each with
 * the role accepting grants them, who invited them and until when the link
 * works. Inviting one again replaces their link, with the same role, and
 * is offered only for a role the caller may grant.
 * Nothing is shown while nobody is invited.
 */
export function InvitedMembers({ project, names, namesPending, onReissued }: InvitedMembersProps) {
  const { i18n, t } = useLingui();
  const { roles, permissions } = useContext(AccessContext);
  const grantable = useMemo(
    () => new Set(grantableRoles(roles, permissions).map((role) => role.name)),
    [roles, permissions],
  );
  const invited = useQuery(projectInvitationsQuery(project));
  const [reissuing, setReissuing] = useState<ProjectInvitation>();
  const now = useNowPassing((invited.data ?? []).map((invitation) => invitation.expires_at));
  const reissue = useProjectInvite(project, (answer) => {
    setReissuing(undefined);
    onReissued(answer);
  });

  const columns = useMemo(() => {
    const columns: TableColumn<ProjectInvitation>[] = [
      {
        id: "member",
        header: t`Member`,
        cell: ({ row }) => (
          <span className="flex flex-col">
            <span className="font-medium">{memberName(row.original)}</span>
            <span className="text-xs text-muted-foreground">{row.original.email}</span>
          </span>
        ),
      },
      {
        id: "role",
        header: t`Role on acceptance`,
        cell: ({ row }) => roleName(i18n, row.original.role),
      },
      {
        id: "invited-by",
        header: t`Invited by`,
        cell: ({ row }) => madeBy(i18n, names, row.original.invited_by),
      },
      {
        id: "expires",
        header: t`Link`,
        cell: ({ row }) => {
          const until = formatDateTime(i18n, row.original.expires_at);
          return invitationExpired(row.original.expires_at, now.getTime()) ? (
            <Badge variant="muted">
              <Trans>Expired on {until}</Trans>
            </Badge>
          ) : (
            <Trans>
              Works until <time dateTime={row.original.expires_at}>{until}</time>
            </Trans>
          );
        },
      },
    ];
    return withRowActions(columns, true, (row) => {
      // Inviting again carries the same role, which the server refuses
      // from an inviter who does not hold every permission it gives.
      if (!grantable.has(row.role)) return null;
      const name = memberName(row);
      return (
        <RowAction
          label={t`Invite ${name} again`}
          icon={MailPlusIcon}
          onClick={() => setReissuing(row)}
        />
      );
    });
  }, [grantable, i18n, names, now, t]);

  if (invited.isError) {
    return <QueryError error={invited.error} onRetry={() => void invited.refetch()} />;
  }
  if (invited.data?.length === 0) return null;

  const name = reissuing ? memberName(reissuing) : "";
  const role = reissuing ? roleName(i18n, reissuing.role) : "";
  return (
    <section className="flex flex-col gap-3" aria-labelledby="invited-members">
      <h2 id="invited-members" className="text-base font-semibold">
        <Trans>Invited members</Trans>
      </h2>
      <Table
        caption={t`Members invited to the project`}
        hideCaption
        columns={columns}
        data={invited.data ?? []}
        loading={invited.isPending || namesPending}
        getRowId={(invitation) => invitation.member}
      />
      <ConfirmDialog
        open={reissuing !== undefined}
        onOpenChange={(open) => {
          if (!open) {
            reissue.reset();
            setReissuing(undefined);
          }
        }}
        title={t`Invite ${name} again`}
        confirmLabel={<Trans>Invite again</Trans>}
        pending={reissue.isPending}
        refusal={reissue.error}
        onConfirm={() =>
          reissuing &&
          reissue.mutate({
            email: reissuing.email,
            display_name: reissuing.display_name,
            role: reissuing.role,
          })
        }
      >
        <p>
          <Trans>
            {name} gets a fresh invitation as {role}, and their earlier link stops working.
          </Trans>
        </p>
      </ConfirmDialog>
    </section>
  );
}
