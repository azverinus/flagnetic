import { Trans, useLingui } from "@lingui/react/macro";

import { RevealSecretDialog } from "@/components/ui/reveal-secret";

/**
 * The signing secret of a subscription, shown the once the platform
 * answers it: when the subscription is created and when it is rotated.
 */
export function WebhookSecretDialog({
  secret,
  rotated,
  onClose,
}: {
  /** The secret, or none while the dialog is closed. */
  secret: string | undefined;
  /** Whether it replaces an earlier one, which keeps signing for a day beside it. */
  rotated: boolean;
  onClose: () => void;
}) {
  const { t } = useLingui();
  return (
    <RevealSecretDialog
      open={secret !== undefined}
      title={t`The webhook's signing secret`}
      value={secret ?? ""}
      kept="sealed"
      onClose={onClose}
    >
      <Trans>
        Every call carries a signature made with this secret, which the receiver checks to know the
        event came from here.
      </Trans>
      {rotated ? (
        <>
          {" "}
          <Trans>
            The secret it replaces keeps signing beside this one for a day, so the receiver can take
            the new one without refusing an event.
          </Trans>
        </>
      ) : null}
    </RevealSecretDialog>
  );
}
