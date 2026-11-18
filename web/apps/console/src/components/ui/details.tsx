import { Trans } from "@lingui/react/macro";
import type { ReactNode } from "react";

import { cn } from "@/lib/cn";

export type DetailsProps = {
  children: ReactNode;
  className?: string;
};

/**
 * What a hint has no room for, an unlock command or a side effect on live
 * sessions, folded under the field and closed until asked for. It is the
 * native disclosure element, so a screen reader announces it as collapsed
 * or expanded and the keyboard opens it without script.
 */
export function Details({ children, className }: DetailsProps) {
  return (
    <details className={cn("text-sm text-muted-foreground", className)}>
      <summary className="w-fit cursor-pointer select-none">
        <Trans>Details</Trans>
      </summary>
      <div className="mt-1">{children}</div>
    </details>
  );
}
