import type { MemberInvitation } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { Dialog, DialogActions } from "@/components/ui/dialog";
import { RevealSecretDialog } from "@/components/ui/reveal-secret";
import { formatDateTime } from "@/i18n/format";
import { memberName } from "@/lib/members";

export type InvitationLinkDialogProps = {
  /** The answer that issued an invitation; none while the dialog is closed. */
  issued: MemberInvitation | undefined;
  /** What issued it, which the title says. */
  kind: "invited" | "reissued" | "reactivated";
  onClose: () => void;
};

/**
 * What became of an invitation just issued. With SMTP configured the
 * instance mails its link to the invited person and the issuer never sees
 * it, so the dialog says where it went; otherwise the link is shown this
 * once, for the issuer to pass on: it opens once, until it expires, and
 * only its hash is kept.
 */
export function InvitationLinkDialog({ issued, kind, onClose }: InvitationLinkDialogProps) {
  const { i18n, t } = useLingui();
  const invitation = issued?.invitation;
  const name = issued ? memberName(issued.member) : "";
  const email = issued?.member.email ?? "";
  const until = invitation ? formatDateTime(i18n, invitation.expires_at) : "";
  const mailed = invitation?.mailed === true;
  const title =
    kind === "reissued"
      ? mailed
        ? t`A new invitation for ${name}`
        : t`A new invitation link for ${name}`
      : kind === "reactivated"
        ? t`${name} is invited again`
        : t`${name} is invited`;

  const after: ReactNode = (
    <>
      {kind === "reissued" ? (
        <p className="mt-2">
          <Trans>Every earlier link for {name} no longer works.</Trans>
        </p>
      ) : null}
      {kind === "reactivated" ? (
        <p className="mt-2">
          {mailed ? (
            <Trans>{name} never set a password, so they join again through the mailed link.</Trans>
          ) : (
            <Trans>{name} never set a password, so they join again through this link.</Trans>
          )}
        </p>
      ) : null}
    </>
  );

  if (mailed) {
    return (
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open) onClose();
        }}
        title={title}
      >
        <div className="text-sm text-muted-foreground">
          <p>
            <Trans>
              The invitation is mailed to {email}. Its link works once, until {until}. If the mail
              does not arrive, issue the invitation again.
            </Trans>
          </p>
          {after}
        </div>
        <DialogActions>
          <Button onClick={onClose}>
            <Trans>Done</Trans>
          </Button>
        </DialogActions>
      </Dialog>
    );
  }

  return (
    <RevealSecretDialog
      open={invitation !== undefined}
      title={title}
      value={invitation?.url ?? ""}
      kept="hash"
      onClose={onClose}
    >
      <p>
        <Trans>
          This instance sends no mail, so pass this link to {name} yourself. It works once, until{" "}
          {until}.
        </Trans>
      </p>
      {after}
    </RevealSecretDialog>
  );
}
