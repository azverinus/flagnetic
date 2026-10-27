import { Trans, useLingui } from "@lingui/react/macro";
import { useBlocker } from "@tanstack/react-router";
import { type ReactNode, useEffect, useRef } from "react";

import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";

export type UnsavedChangesBarProps = {
  /** Whether the form holds changes not saved yet; the bar shows only then. */
  dirty: boolean;
  onDiscard: () => void;
  /** Submits the form; omitted when the form's own button saves. */
  onSave?: () => void;
  /** What the save button says; Save unless the form proposes its changes instead. */
  saveLabel?: ReactNode;
  pending?: boolean;
  /**
   * Whether a navigation keeps the form on screen, as opening a panel
   * beside it through the address does, so it is not asked about.
   */
  keeps?: (from: Place, to: Place) => boolean;
};

/** Where a navigation starts or ends: the path and its search parameters. */
type Place = { pathname: string; search: object };

/** The space between the bar and a toast above it. */
const toastGap = 16;

/**
 * The bar a form shows while it holds unsaved changes. It sticks to the
 * bottom of the main column, which is the scrolling container beside the
 * sidebar, so it never covers the sidebar; toasts are lifted above it while
 * it shows. Leaving the page while it shows asks first, inside the console
 * and when the browser closes the tab.
 */
export function UnsavedChangesBar({
  dirty,
  onDiscard,
  onSave,
  saveLabel,
  pending,
  keeps,
}: UnsavedChangesBarProps) {
  const { t } = useLingui();
  const bar = useRef<HTMLDivElement>(null);
  const blocker = useBlocker({
    shouldBlockFn: ({ current, next }) => dirty && !keeps?.(current, next),
    enableBeforeUnload: () => dirty,
    withResolver: true,
  });

  useEffect(() => {
    if (!dirty || !bar.current) return;
    const root = document.documentElement;
    const element = bar.current;
    const lift = () =>
      root.style.setProperty("--toast-offset", `${element.offsetHeight + toastGap}px`);
    lift();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(lift);
    observer?.observe(element);
    return () => {
      observer?.disconnect();
      root.style.removeProperty("--toast-offset");
    };
  }, [dirty]);

  return (
    <>
      {dirty ? (
        <div
          ref={bar}
          role="region"
          aria-label={t`Unsaved changes`}
          className="sticky bottom-0 z-10 mt-6 flex flex-wrap items-center justify-between gap-3 border-t bg-background/95 px-6 py-3 shadow-[0_-1px_3px_rgba(0,0,0,0.06)] backdrop-blur"
        >
          <p className="text-sm font-medium">
            <Trans>You have unsaved changes.</Trans>
          </p>
          <div className="flex gap-2">
            <Button type="button" variant="outline" onClick={onDiscard} disabled={pending}>
              <Trans>Discard</Trans>
            </Button>
            {onSave ? (
              <Button type="button" onClick={onSave} disabled={pending}>
                {saveLabel ?? <Trans>Save</Trans>}
              </Button>
            ) : null}
          </div>
        </div>
      ) : null}
      <ConfirmDialog
        open={blocker.status === "blocked"}
        onOpenChange={(open) => {
          if (!open && blocker.status === "blocked") blocker.reset();
        }}
        title={t`Leave without saving?`}
        confirmLabel={<Trans>Leave</Trans>}
        destructive
        onConfirm={() => {
          if (blocker.status === "blocked") blocker.proceed();
        }}
      >
        <p>
          <Trans>Your changes on this page will be lost.</Trans>
        </p>
      </ConfirmDialog>
    </>
  );
}
