import type { IssuedPasswordReset } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";

import { RevealSecretDialog } from "@/components/ui/reveal-secret";
import { formatDateTime } from "@/i18n/format";

export type PasswordResetLinkDialogProps = {
  /** The password reset just issued; none while the dialog is closed. */
  issued: IssuedPasswordReset | undefined;
  /** Who the link resets the password of, as the screen calls them. */
  name: string;
  onClose: () => void;
};

/**
 * The one showing of a password reset link an instance admin issued. The
 * instance never mails it, so the admin passes it on themselves; it opens
 * once, for an hour, and only its hash is kept.
 */
export function PasswordResetLinkDialog({ issued, name, onClose }: PasswordResetLinkDialogProps) {
  const { i18n, t } = useLingui();
  const until = issued ? formatDateTime(i18n, issued.expires_at) : "";
  return (
    <RevealSecretDialog
      open={issued !== undefined}
      title={t`A password reset link for ${name}`}
      value={issued?.url ?? ""}
      kept="hash"
      onClose={onClose}
    >
      <p>
        <Trans>
          The instance does not mail this link, so pass it to {name} yourself, over a channel only
          they read. It works once, until {until}.
        </Trans>
      </p>
    </RevealSecretDialog>
  );
}
