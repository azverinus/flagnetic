import { useLingui } from "@lingui/react/macro";
import { XIcon } from "lucide-react";
import { Dialog as DialogPrimitive } from "radix-ui";
import type { ReactNode } from "react";

import { cn } from "@/lib/cn";
import { useReturnFocus } from "@/lib/return-focus";

export type SheetProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The sheet's accessible name, rendered as its heading. */
  title: string;
  description?: ReactNode;
  className?: string;
  children: ReactNode;
};

/**
 * A panel sliding in from the right edge, for work beside the page, such
 * as editing one row of a table: a modal dialog in every other respect.
 */
export function Sheet({ open, onOpenChange, title, description, className, children }: SheetProps) {
  const { t } = useLingui();
  const focus = useReturnFocus();
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/50" />
        <DialogPrimitive.Content
          {...focus}
          className={cn(
            "fixed inset-y-0 right-0 z-50 flex h-dvh w-full max-w-md flex-col gap-4 overflow-y-auto border-l bg-background p-6 text-foreground shadow-lg",
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
            <DialogPrimitive.Close
              aria-label={t`Close`}
              className="-mt-1 -mr-2 inline-flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-accent-foreground"
            >
              <XIcon aria-hidden className="size-4" />
            </DialogPrimitive.Close>
          </div>
          {children}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
