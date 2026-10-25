import type { I18n } from "@lingui/core";
import { Trans, useLingui } from "@lingui/react/macro";
import { CircleAlertIcon } from "lucide-react";
import { type ReactNode, useId, useState } from "react";

import { Button } from "@/components/ui/button";
import { Dialog, DialogActions } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError } from "@/lib/errors";
import { problemMessage } from "@/lib/problem-messages";

export type ConfirmDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  confirmLabel: ReactNode;
  destructive?: boolean;
  pending?: boolean;
  /** A refusal, shown here, keeping the dialog open. */
  refusal?: unknown;
  /** Explains the refusal where the general message would mislead, as for an archived experiment. */
  refusalMessage?: (i18n: I18n, error: ApiError) => string;
  /** What the member must type out before the action is offered. */
  typeToConfirm?: string;
  onConfirm: () => void;
  /** The consequence of the action, said plainly. */
  children: ReactNode;
};

/**
 * The one confirmation for every action that cannot be taken back. It names
 * the consequence rather than asking whether the member is sure, and the
 * most dangerous actions ask for a key to be typed out first.
 */
export function ConfirmDialog({ open, onOpenChange, title, ...rest }: ConfirmDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={title}>
      {/* Mounted only while open, so a second confirmation starts unanswered. */}
      {open ? <Confirmation onCancel={() => onOpenChange(false)} {...rest} /> : null}
    </Dialog>
  );
}

function Confirmation({
  confirmLabel,
  destructive = false,
  pending = false,
  refusal,
  refusalMessage = problemMessage,
  typeToConfirm,
  onConfirm,
  onCancel,
  children,
}: Omit<ConfirmDialogProps, "open" | "onOpenChange" | "title"> & { onCancel: () => void }) {
  const { i18n } = useLingui();
  const [typed, setTyped] = useState("");
  const field = useId();
  const ready = typeToConfirm === undefined || typed.trim() === typeToConfirm;
  const error = refusal instanceof ApiError ? refusal : undefined;

  return (
    <>
      <div className="flex flex-col gap-2 text-sm text-muted-foreground">{children}</div>
      {typeToConfirm === undefined ? null : (
        <div className="flex flex-col gap-2">
          <Label htmlFor={field}>
            <Trans>Type {typeToConfirm} to confirm</Trans>
          </Label>
          <Input
            id={field}
            autoComplete="off"
            value={typed}
            onChange={(event) => setTyped(event.target.value)}
          />
        </div>
      )}
      {error ? (
        <p role="alert" className="flex items-start gap-2 text-sm">
          <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
          {refusalMessage(i18n, error)}
        </p>
      ) : null}
      <DialogActions>
        <Button variant="outline" onClick={onCancel}>
          <Trans>Cancel</Trans>
        </Button>
        <Button
          variant={destructive ? "destructive" : "default"}
          onClick={onConfirm}
          disabled={pending || !ready}
        >
          {confirmLabel}
        </Button>
      </DialogActions>
    </>
  );
}
