import {
  deactivateMember,
  type Member,
  type MemberInvitation,
  type MemberPatch,
  reactivateMember,
  reissueInvitation,
  updateMember,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";

import { InvitationLinkDialog } from "@/components/invitation-link-dialog";
import { MemberForm, type MemberValues } from "@/components/member-form";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog } from "@/components/ui/dialog";
import { unwrap } from "@/lib/errors";
import { type MemberAction, memberActions, memberName } from "@/lib/members";
import { meQuery } from "@/lib/queries";
import { forgetsSecret } from "@/lib/mutations";

/** A change of a member confirmed in a dialog, by the action that asks for it. */
type Confirmed = Exclude<MemberAction, "edit">;

export type MemberActionsProps = {
  member: Member;
  /** Whether the member is the instance admin looking at them. */
  self: boolean;
};

/**
 * What an instance admin does to one member: edit them, make them instance
 * admin or not, replace their invitation link, and deactivate or
 * reactivate them. Each change but editing is confirmed first, saying what
 * it does; a refusal, such as the last instance admin's, is explained in
 * the dialog that asked for it.
 */
export function MemberActions({ member, self }: MemberActionsProps) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [confirming, setConfirming] = useState<Confirmed>();
  const [issued, setIssued] = useState<{
    answer: MemberInvitation;
    kind: "reissued" | "reactivated";
  }>();
  const name = memberName(member);

  const doneMessage: Record<Confirmed, string> = {
    promote: t`${name} is an instance admin.`,
    demote: t`${name} is no longer an instance admin.`,
    reissue: t`A new invitation link is issued.`,
    deactivate: t`${name} is deactivated.`,
    reactivate: t`${name} is reactivated.`,
  };
  const changed = async () => {
    // The member's own screens, the list and their sessions and tokens
    // all read under the members' key; an instance admin changing
    // themselves also changes what they may open.
    await queryClient.invalidateQueries({ queryKey: ["members"] });
    if (self) await queryClient.invalidateQueries({ queryKey: meQuery.queryKey });
  };

  const edit = useMutation({
    mutationFn: (body: MemberPatch) => unwrap(updateMember({ path: { member: member.id }, body })),
    meta: { toast: false },
    onSuccess: async () => {
      await changed();
      setEditing(false);
      toast.success(t`The member is saved.`);
    },
  });
  const change = useMutation({
    mutationFn: async (action: Confirmed): Promise<MemberInvitation | undefined> => {
      const path = { member: member.id };
      switch (action) {
        case "promote":
        case "demote":
          await unwrap(updateMember({ path, body: { instance_admin: action === "promote" } }));
          return undefined;
        case "reissue":
          return unwrap(reissueInvitation({ path }));
        case "deactivate":
          await unwrap(deactivateMember({ path }));
          return undefined;
        case "reactivate":
          return unwrap(reactivateMember({ path }));
      }
    },
    ...forgetsSecret,
    meta: { toast: false },
    onSuccess: async (answer, action) => {
      await changed();
      setConfirming(undefined);
      // The link lives in the dialog that shows it, never in the
      // mutation's state: reset detaches the mutation, and with no cache
      // time the mutation cache drops it, answer included, so the link is
      // gone once that dialog closes.
      change.reset();
      if (answer?.invitation) {
        setIssued({ answer, kind: action === "reissue" ? "reissued" : "reactivated" });
        return;
      }
      toast.success(doneMessage[action]);
    },
  });

  const actionLabel: Record<MemberAction, string> = {
    edit: t`Edit`,
    promote: t`Make instance admin`,
    demote: t`Revoke instance admin`,
    reissue: t`Issue a new invitation link`,
    deactivate: t`Deactivate`,
    reactivate: t`Reactivate`,
  };
  const confirmation: Record<Confirmed, { title: string; consequence: string; confirm: string }> = {
    promote: {
      title: t`Make ${name} an instance admin`,
      consequence: t`An instance admin administers every project, every member and the instance's settings.`,
      confirm: t`Make instance admin`,
    },
    demote: {
      title: t`Revoke instance admin from ${name}`,
      consequence: self
        ? t`You keep only the roles your grants give you, and lose the instance's screens at once.`
        : t`${name} keeps only the roles their grants give them in each project.`,
      confirm: t`Revoke instance admin`,
    },
    reissue: {
      title: t`Issue a new invitation link for ${name}`,
      consequence: t`Every earlier link for ${name} stops working. The new one is shown once.`,
      confirm: t`Issue link`,
    },
    deactivate: {
      title: t`Deactivate ${name}`,
      consequence: self
        ? t`Deactivation ends every session and API token of yours at once, this one included, and you cannot sign in until another instance admin reactivates you.`
        : t`Deactivation ends every session and API token of ${name} at once, and they cannot sign in until reactivated. Project tokens they created keep working.`,
      confirm: t`Deactivate`,
    },
    reactivate: {
      title: t`Reactivate ${name}`,
      consequence: t`${name} can sign in again. What deactivation revoked stays revoked: their sessions and API tokens do not come back.`,
      confirm: t`Reactivate`,
    },
  };
  const confirm = confirming ? confirmation[confirming] : undefined;

  return (
    <>
      {memberActions(member).map((action) => (
        <Button
          key={action}
          variant="outline"
          onClick={() => (action === "edit" ? setEditing(true) : setConfirming(action))}
        >
          {actionLabel[action]}
        </Button>
      ))}

      <Dialog
        open={editing}
        onOpenChange={(open) => {
          if (!open) {
            edit.reset();
            setEditing(false);
          }
        }}
        title={t`Edit ${name}`}
      >
        {editing ? (
          <MemberForm
            initial={{ email: member.email, display_name: member.display_name }}
            submitLabel={<Trans>Save</Trans>}
            pending={edit.isPending}
            refusal={edit.error}
            onCancel={() => {
              edit.reset();
              setEditing(false);
            }}
            onSubmit={(values: MemberValues) => {
              // Only what changed is sent, so an unchanged field is never
              // judged again.
              const body: MemberPatch = {};
              if (values.email !== member.email) body.email = values.email;
              if (values.display_name !== member.display_name) {
                body.display_name = values.display_name;
              }
              // Nothing changed is nothing to save.
              if (Object.keys(body).length === 0) {
                setEditing(false);
                return;
              }
              edit.mutate(body);
            }}
          />
        ) : null}
      </Dialog>
      <ConfirmDialog
        open={confirming !== undefined}
        onOpenChange={(open) => {
          if (!open) {
            change.reset();
            setConfirming(undefined);
          }
        }}
        title={confirm?.title ?? ""}
        confirmLabel={confirm?.confirm ?? ""}
        destructive={confirming === "deactivate" || confirming === "demote"}
        pending={change.isPending}
        refusal={change.error}
        onConfirm={() => confirming && change.mutate(confirming)}
      >
        <p>{confirm?.consequence}</p>
      </ConfirmDialog>
      <InvitationLinkDialog
        issued={issued?.answer}
        kind={issued?.kind ?? "reissued"}
        onClose={() => setIssued(undefined)}
      />
    </>
  );
}
