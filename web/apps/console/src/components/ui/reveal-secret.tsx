import { Trans } from "@lingui/react/macro";
import { CheckIcon, CopyIcon } from "lucide-react";
import { type ReactNode, useId, useState } from "react";

import { Button } from "@/components/ui/button";
import { Dialog, DialogActions } from "@/components/ui/dialog";

export type RevealSecretProps = {
  open: boolean;
  title: string;
  /** The secret, shown this once. */
  value: string;
  /**
   * How the instance keeps the secret, which decides what a member does on
   * losing it: `hash` for one only checked against what is presented (an
   * API token, an invitation), so nobody can show it again and it is
   * replaced; `sealed` for one the instance must use itself (a webhook's
   * signing secret), kept encrypted but never shown again either, so it
   * is rotated.
   */
  kept: "hash" | "sealed";
  /** What the secret is for and what to do with it. */
  children?: ReactNode;
  onClose: () => void;
};

/**
 * The one place a secret's value (an API token, an invitation) exists
 * in the console. It lives while the dialog is open and nowhere else: not
 * in a query cache, browser storage, the address or a log. The dialog
 * cannot be dismissed until the member says the value is stored, because
 * there is no second showing.
 */
export function RevealSecretDialog({ open, title, ...reveal }: RevealSecretProps) {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) reveal.onClose();
      }}
      title={title}
      dismissible={false}
    >
      {/* Mounted only while open, so a second reveal starts unacknowledged. */}
      {open ? <Reveal {...reveal} /> : null}
    </Dialog>
  );
}

function Reveal({ value, kept, children, onClose }: Omit<RevealSecretProps, "open" | "title">) {
  const [stored, setStored] = useState(false);
  const [copy, setCopy] = useState<"idle" | "copied" | "refused">("idle");
  const acknowledgement = useId();

  const toClipboard = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopy("copied");
    } catch {
      // A browser that refuses the clipboard leaves the member the value
      // itself, which is on the screen in front of them.
      setCopy("refused");
    }
  };

  return (
    <>
      {children ? <div className="text-sm text-muted-foreground">{children}</div> : null}
      <p className="text-sm text-muted-foreground">
        {kept === "hash" ? (
          <Trans>
            This is the only time it is shown. Only a hash of it is kept, so if it is lost, replace
            it.
          </Trans>
        ) : (
          <Trans>
            This is the only time it is shown. It is kept encrypted and never shown again, so if it
            is lost, rotate it.
          </Trans>
        )}
      </p>
      <div className="flex items-start gap-2 rounded-md border bg-muted/40 p-3">
        <code className="min-w-0 flex-1 font-mono text-sm break-all select-all">{value}</code>
        <Button variant="outline" size="sm" onClick={() => void toClipboard()}>
          <CopyIcon aria-hidden />
          <Trans>Copy</Trans>
        </Button>
      </div>
      <p role="status" className="min-h-4 text-xs text-muted-foreground">
        {copy === "copied" ? (
          <span className="inline-flex items-center gap-1 text-primary">
            <CheckIcon aria-hidden className="size-3" />
            <Trans>Copied to the clipboard.</Trans>
          </span>
        ) : copy === "refused" ? (
          <Trans>The browser refused the clipboard. Select the value and copy it by hand.</Trans>
        ) : null}
      </p>
      <div className="flex items-center gap-2">
        <input
          id={acknowledgement}
          type="checkbox"
          checked={stored}
          onChange={(event) => setStored(event.target.checked)}
          className="size-4 rounded border-input accent-primary"
        />
        <label htmlFor={acknowledgement} className="text-sm">
          <Trans>I have stored it somewhere safe</Trans>
        </label>
      </div>
      <DialogActions>
        <Button onClick={onClose} disabled={!stored}>
          <Trans>Close</Trans>
        </Button>
      </DialogActions>
    </>
  );
}
