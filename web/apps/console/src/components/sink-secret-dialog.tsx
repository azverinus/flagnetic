import { Trans, useLingui } from "@lingui/react/macro";

import { RevealSecretDialog } from "@/components/ui/reveal-secret";

/**
 * A webhook sink's signing secret, shown the once the platform answers it:
 * when the sink is created and when it is rotated.
 */
export function SinkSecretDialog({
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
      title={t`The sink's signing secret`}
      value={secret ?? ""}
      kept="sealed"
      onClose={onClose}
    >
      <Trans>
        Every batch carries a signature made with this secret, which the receiver checks to know the
        events came from here.
      </Trans>
      {rotated ? (
        <>
          {" "}
          <Trans>
            The secret it replaces keeps signing beside this one for a day, so the receiver can take
            the new one without refusing a batch.
          </Trans>
        </>
      ) : null}
    </RevealSecretDialog>
  );
}
