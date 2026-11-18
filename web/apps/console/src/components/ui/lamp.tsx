import { Trans } from "@lingui/react/macro";
import type { ComponentProps } from "react";

import { cn } from "@/lib/cn";

export type LampProps = Omit<ComponentProps<"span">, "children"> & {
  /** Whether the environment is serving the flag. */
  on: boolean;
};

/**
 * The state lamp: a dot filled in the state colour with a soft ring when
 * the environment serves, hollow and muted when it does not, and always
 * followed by the word, so the state never rests on colour alone. It names
 * an environment's serving state and nothing else.
 */
export function Lamp({ on, className, ...props }: LampProps) {
  return (
    <span {...props} className={cn("inline-flex items-center gap-1.5 text-sm", className)}>
      <span
        aria-hidden
        className={cn(
          "size-2 shrink-0 rounded-full",
          on
            ? "bg-state-on ring-2 ring-state-on/25"
            : "border border-muted-foreground/60 bg-transparent",
        )}
      />
      {on ? (
        <Trans>On</Trans>
      ) : (
        <span className="text-muted-foreground">
          <Trans>Off</Trans>
        </span>
      )}
    </span>
  );
}
