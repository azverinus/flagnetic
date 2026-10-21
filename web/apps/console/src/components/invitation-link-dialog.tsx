import type { MemberInvitation } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";

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
 * The one showing of an invitation's link. The instance sends no mail, so
 * the instance admin passes the link on themselves; it opens once, until
 * it expires, and only its hash is kept.
 */
export function InvitationLinkDialog({ issued, kind, onClose }: InvitationLinkDialogProps) {
  const { i18n, t } = useLingui();
  const invitation = issued?.invitation;
  const name = issued ? memberName(issued.member) : "";
  const until = invitation ? formatDateTime(i18n, invitation.expires_at) : "";
  const title =
    kind === "reissued"
      ? t`A new invitation link for ${name}`
      : kind === "reactivated"
        ? t`${name} is invited again`
        : t`${name} is invited`;

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
      {kind === "reissued" ? (
        <p className="mt-2">
          <Trans>Every earlier link for {name} no longer works.</Trans>
        </p>
      ) : null}
      {kind === "reactivated" ? (
        <p className="mt-2">
          <Trans>{name} never set a password, so they join again through this link.</Trans>
        </p>
      ) : null}
    </RevealSecretDialog>
  );
}
