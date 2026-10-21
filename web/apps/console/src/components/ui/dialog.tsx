import { useLingui } from "@lingui/react/macro";
import { XIcon } from "lucide-react";
import { Dialog as DialogPrimitive } from "radix-ui";
import type { ReactNode } from "react";

import { cn } from "@/lib/cn";
import { useReturnFocus } from "@/lib/return-focus";

export type DialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The dialog's accessible name, rendered as its heading. */
  title: string;
  /** A sentence under the heading, read out as the dialog's description. */
  description?: ReactNode;
  /**
   * False for a dialog the member must act on before it goes: Escape and a
   * click outside are refused and the frame has no close button. The
   * one-time reveal of a secret is one.
   */
  dismissible?: boolean;
  className?: string;
  children: ReactNode;
};

/**
 * A modal dialog: focus moves in and is trapped there, Escape closes it,
 * and focus returns to what opened it.
 */
export function Dialog({
  open,
  onOpenChange,
  title,
  description,
  dismissible = true,
  className,
  children,
}: DialogProps) {
  const { t } = useLingui();
  const focus = useReturnFocus();
  const refuse = (event: Event) => {
    if (!dismissible) event.preventDefault();
  };
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/50" />
        <DialogPrimitive.Content
          {...focus}
          onEscapeKeyDown={refuse}
          onPointerDownOutside={refuse}
          onInteractOutside={refuse}
          className={cn(
            "fixed top-1/2 left-1/2 z-50 flex max-h-[calc(100dvh-2rem)] w-[min(32rem,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 flex-col gap-4 overflow-y-auto rounded-lg border bg-background p-6 text-foreground shadow-lg",
            className,
          )}
        >
          <div className="flex items-start justify-between gap-4">
            <div className="flex min-w-0 flex-col gap-1">
              <DialogPrimitive.Title className="text-lg font-semibold">
                {title}
              </DialogPrimitive.Title>
              {description ? (
                <DialogPrimitive.Description className="text-sm text-muted-foreground">
                  {description}
                </DialogPrimitive.Description>
              ) : null}
            </div>
            {dismissible ? (
              <DialogPrimitive.Close
                aria-label={t`Close`}
                className="-mt-1 -mr-2 inline-flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-accent-foreground"
              >
                <XIcon aria-hidden className="size-4" />
              </DialogPrimitive.Close>
            ) : null}
          </div>
          {children}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

/** Where a dialog's actions sit: right-aligned, the one it came for rightmost. */
export function DialogActions({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap justify-end gap-2">{children}</div>;
}
