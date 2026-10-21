import type { ReactNode } from "react";

import { cn } from "@/lib/cn";

/**
 * The page of someone not signed in yet, signing in or accepting an
 * invitation: one card in the middle of the screen, under the product's
 * name, with nothing of the console around it.
 */
export function SignedOutCard({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <main className="flex min-h-dvh items-center justify-center bg-muted/40 p-4">
      <div
        className={cn(
          "flex w-full flex-col gap-6 rounded-lg border bg-card p-6 shadow-sm",
          className,
        )}
      >
        <div className="flex items-center gap-2">
          <img src="/favicon.svg" alt="" className="size-7" />
          <span className="text-lg font-semibold">Flagnetic</span>
        </div>
        {children}
      </div>
    </main>
  );
}
